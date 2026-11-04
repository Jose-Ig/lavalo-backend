#include <Arduino.h>
#include <WiFi.h>
#include <NetworkClientSecure.h>
#include <HTTPClient.h>
#include <ArduinoJson.h>
#include <esp_timer.h>
#include <time.h>
#include "trusted_ca.h"
#if __has_include("secrets.h")
#include "secrets.h"
#else
#include "secrets.example.h"
#endif

// LED prototype only: active HIGH on P25/P26. No mains equipment connected.
constexpr int HYDRO_PIN = 25;
constexpr int VACUUM_PIN = 26;
constexpr int64_t MAX_SESSION_MS = 12LL * 60 * 60 * 1000;
portMUX_TYPE stateMux = portMUX_INITIALIZER_UNLOCKED;
int64_t deadlineUs = 0;
uint64_t currentRevision = 0, appliedRevision = 0, closedRevision = 0;
bool outputOn = false;
String commandID;
String bootID;
bool configured = false;

// Dedicated task owns the outputs. HTTPS/NTP/Wi-Fi may block; cutoff does not.
void outputTask(void*) {
  for (;;) {
    int64_t now = esp_timer_get_time();
    portENTER_CRITICAL(&stateMux);
    bool on = deadlineUs > now;
    if (!on && deadlineUs != 0) {
      closedRevision = currentRevision;
      deadlineUs = 0;
    }
    digitalWrite(HYDRO_PIN, on ? HIGH : LOW);
    digitalWrite(VACUUM_PIN, on ? HIGH : LOW);
    outputOn = on;
    appliedRevision = currentRevision;
    portEXIT_CRITICAL(&stateMux);
    vTaskDelay(pdMS_TO_TICKS(10));
  }
}
void stopOutputs() {
  portENTER_CRITICAL(&stateMux);
  deadlineUs = 0;
  portEXIT_CRITICAL(&stateMux);
}
String requestID() {
  uint32_t a = esp_random(), b = esp_random(), c = esp_random(), d = esp_random();
  char out[37];
  snprintf(out, sizeof(out), "%08lx-%04lx-4%03lx-8%03lx-%04lx%08lx",
    (unsigned long)a, (unsigned long)(b >> 16), (unsigned long)(b & 0xfff),
    (unsigned long)(c & 0xfff), (unsigned long)(c >> 16), (unsigned long)d);
  return String(out);
}
int postJSON(const char* path, const String& body, String& response) {
  NetworkClientSecure client;
  client.setCACert(UASH_CA);
  client.setHandshakeTimeout(5);
  HTTPClient http;
  http.setConnectTimeout(3500);
  http.setTimeout(3500);
  http.setFollowRedirects(HTTPC_DISABLE_FOLLOW_REDIRECTS);
  if (!http.begin(client, String(UASH_API_URL) + path)) return -1;
  http.addHeader("Content-Type", "application/json");
  http.addHeader("Authorization", String("Bearer ") + UASH_DEVICE_TOKEN);
  int status = http.POST(body);
  if (status == 200) {
    if (http.getSize() > 4096) { http.end(); return -2; }
    // Bound even chunked responses rather than allocating arbitrary remote data.
    class BoundedSink : public Stream {
      public:
        String text;
        bool overflow = false;
        int available() override { return 0; }
        int read() override { return -1; }
        int peek() override { return -1; }
        void flush() override {}
        size_t write(uint8_t c) override {
          if (text.length() >= 4096) { overflow = true; return 0; }
          text += (char)c; return 1;
        }
    } sink;
    if (http.writeToStream(&sink) < 0 || sink.overflow) { http.end(); return -2; }
    response = sink.text;
  }
  http.end();
  return status;
}
void pollBackend() {
  String nonce = requestID();
  JsonDocument request;
  request["request_id"] = nonce;
  request["boot_id"] = bootID;
  String body, response;
  serializeJson(request, body);
  int64_t startedUs = esp_timer_get_time();
  int status = postJSON("/controllers/poll", body, response);
  if (status == 401 || status == 403) {
    stopOutputs(); Serial.println("Acceso del dispositivo rechazado. Salidas apagadas."); return;
  }
  if (status != 200) { Serial.printf("Sin respuesta valida del backend (%d).\n", status); return; }
  JsonDocument doc;
  if (deserializeJson(doc, response)) return;
  JsonObjectConst data = doc["data"].as<JsonObjectConst>();
  if (data["request_id"].as<String>() != nonce || !data["on"].is<bool>() ||
      !data["revision"].is<uint64_t>() || !data["server_ms"].is<int64_t>() ||
      !data["end_ms"].is<int64_t>() || !data["command_id"].is<const char*>()) return;
  bool on = data["on"].as<bool>();
  uint64_t revision = data["revision"].as<uint64_t>();
  String id = data["command_id"].as<String>();
  int64_t remaining = data["end_ms"].as<int64_t>() - data["server_ms"].as<int64_t>();
  if (id.length() != 36 || revision == 0 || (on && (remaining <= 0 || remaining > MAX_SESSION_MS))) return;
  int64_t candidate = startedUs + remaining * 1000; // Conservative: includes transit/processing time.
  portENTER_CRITICAL(&stateMux);
  bool stale = revision < currentRevision || (on && revision <= closedRevision);
  portEXIT_CRITICAL(&stateMux);
  if (stale || (revision == currentRevision && commandID.length() && id != commandID)) return;
  bool same = id == commandID;
  commandID = id;
  portENTER_CRITICAL(&stateMux);
  currentRevision = revision;
  if (!on) deadlineUs = 0;
  else if (!same || deadlineUs == 0) deadlineUs = candidate;
  else if (candidate < deadlineUs) deadlineUs = candidate; // Repeated polling never restarts the timer.
  portEXIT_CRITICAL(&stateMux);
  delay(30); // Allow output task to apply, without blocking its cutoff.
  portENTER_CRITICAL(&stateMux);
  bool applied = outputOn == on && appliedRevision == revision;
  portEXIT_CRITICAL(&stateMux);
  if (!applied) return;
  JsonDocument ack;
  ack["command_id"] = id;
  ack["on"] = on;
  body = ""; response = ""; serializeJson(ack, body);
  int result = postJSON("/controllers/ack", body, response);
  if (result == 401 || result == 403 || result == 409) stopOutputs();
  Serial.printf("Salidas: %s. Confirmacion: %d\n", on ? "ENCENDIDAS" : "APAGADAS", result);
}
void setup() {
  // Set the output latch LOW before enabling outputs.
  digitalWrite(HYDRO_PIN, LOW); digitalWrite(VACUUM_PIN, LOW);
  pinMode(HYDRO_PIN, OUTPUT); pinMode(VACUUM_PIN, OUTPUT);
  Serial.begin(115200);
  if (xTaskCreate(outputTask, "uash-cutoff", 2048, nullptr, 2, nullptr) != pdPASS) {
    Serial.println("No se pudo iniciar el temporizador. Reiniciar placa.");
    while (true) delay(1000);
  }
  bootID = requestID();
  configured = strlen(UASH_DEVICE_TOKEN) == 64 && String(UASH_API_URL).startsWith("https://") &&
    strlen(WIFI_SSID) && strcmp(WIFI_SSID, "CONFIGURAR_WIFI") != 0;
  if (!configured) { Serial.println("Completar secrets.h antes de usar el controlador."); return; }
  WiFi.mode(WIFI_STA);
  WiFi.setAutoReconnect(true);
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  configTime(0, 0, "pool.ntp.org", "time.google.com");
  Serial.println("UASH HTTPS - conectando WiFi y sincronizando reloj.");
}
void loop() {
  if (!configured) { delay(1000); return; }
  if (WiFi.status() != WL_CONNECTED || time(nullptr) < 1767225600) {
    Serial.println("Esperando WiFi/reloj para verificar HTTPS."); delay(2500); return;
  }
  pollBackend();
  delay(2500);
}
