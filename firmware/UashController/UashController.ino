#include <Arduino.h>
#include <WiFi.h>
#include <NetworkClientSecure.h>
#include <HTTPClient.h>
#include <ArduinoJson.h>
#include <Preferences.h>
#include <atomic>
#include <esp_timer.h>
#include <time.h>
#include <sys/time.h>
#include "deadline.h"
#include "recovery.h"
#include "trusted_ca.h"
#if __has_include("secrets.h")
#include "secrets.h"
#else
#include "secrets.example.h"
#endif

// LED prototype only: active HIGH on P25/P26. No mains equipment connected.
constexpr int HYDRO_PIN = 25;
constexpr int VACUUM_PIN = 26;
portMUX_TYPE stateMux = portMUX_INITIALIZER_UNLOCKED;
SemaphoreHandle_t persistenceMutex;
Preferences storage;
uash::RecoveryRecord currentRecord{};
bool recordValid=false, recordDirty=false, tracking=false;
std::atomic<bool> storageHealthy{false};
bool outputOn=false, configured=false, bootstrapped=false;
int64_t deadlineUs=0;
uint64_t currentRevision=0, appliedRevision=0, closedRevision=0;
String commandID, bootID;

// Only this task touches GPIO. HTTPS and checkpoint writes cannot block cutoff.
void outputTask(void*) {
  for (;;) {
    const int64_t now=esp_timer_get_time();
    portENTER_CRITICAL(&stateMux);
    const bool on=deadlineUs>now;
    if (!on && deadlineUs!=0) {
      closedRevision=currentRevision;
      deadlineUs=0;
    }
    digitalWrite(HYDRO_PIN,on?HIGH:LOW);
    digitalWrite(VACUUM_PIN,on?HIGH:LOW);
    outputOn=on;
    appliedRevision=currentRevision;
    portEXIT_CRITICAL(&stateMux);
    vTaskDelay(pdMS_TO_TICKS(10));
  }
}

void stopOutputs() {
  const int64_t now=esp_timer_get_time();
  portENTER_CRITICAL(&stateMux);
  if (recordValid && tracking) {
    currentRecord.remainingMs=uash::remainingAt(deadlineUs,now);
    currentRecord.completed=currentRecord.remainingMs==0;
    recordDirty=true;
  }
  tracking=false;
  deadlineUs=0;
  portEXIT_CRITICAL(&stateMux);
}

bool snapshotRecord(uash::RecoveryRecord& record) {
  const int64_t now=esp_timer_get_time();
  portENTER_CRITICAL(&stateMux);
  const bool valid=recordValid;
  memcpy(&record,&currentRecord,sizeof(record));
  if (valid && tracking) {
    record.remainingMs=uash::remainingAt(deadlineUs,now);
    record.completed=record.remainingMs==0;
  }
  portEXIT_CRITICAL(&stateMux);
  return valid;
}

void checkpointTask(void*) {
  int64_t lastWrite=esp_timer_get_time();
  for (;;) {
    vTaskDelay(pdMS_TO_TICKS(250));
    if (xSemaphoreTake(persistenceMutex,pdMS_TO_TICKS(10))!=pdTRUE) continue;
    uash::RecoveryRecord sample{};
    const bool valid=snapshotRecord(sample);
    const int64_t now=esp_timer_get_time();
    bool changed=false;
    portENTER_CRITICAL(&stateMux);
    changed=valid && (recordDirty || sample.remainingMs!=currentRecord.remainingMs || sample.completed!=currentRecord.completed);
    portEXIT_CRITICAL(&stateMux);
    if (valid && changed && (sample.completed || now-lastWrite>=uash::kCheckpointIntervalUs)) {
      sample.sequence++;
      sample.checksum=uash::recordChecksum(sample);
      if (storage.putBytes("checkpoint",&sample,sizeof(sample))==sizeof(sample)) {
        portENTER_CRITICAL(&stateMux);
        if (!tracking && currentRecord.remainingMs<sample.remainingMs) {
          // A concurrent stop captured a newer value while flash was writing.
          currentRecord.sequence=sample.sequence;
          recordDirty=true;
        } else {
          memcpy(&currentRecord,&sample,sizeof(sample));
          recordDirty=false;
        }
        portEXIT_CRITICAL(&stateMux);
        lastWrite=now;
      } else {
        storageHealthy=false;
        stopOutputs();
        Serial.println("No se pudo guardar el tiempo. Salidas apagadas; revisar almacenamiento.");
      }
    }
    xSemaphoreGive(persistenceMutex);
  }
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

void appendCheckpoint(JsonObject object,const uash::RecoveryRecord& record) {
  object["session_id"]=record.session;
  object["execution_id"]=record.execution;
  object["revision"]=record.revision;
  object["sequence"]=record.sequence;
  object["remaining_ms"]=record.remainingMs;
  object["completed"]=record.completed!=0;
}

bool applyCommand(JsonObjectConst data) {
  if (data["boot_id"].as<String>()!=bootID || data["protocol"].as<int>()!=2 ||
      !data["on"].is<bool>() || !data["revision"].is<uint64_t>() ||
      !data["end_ms"].is<int64_t>() || !data["server_ms"].is<int64_t>()) return false;
  const String id=data["command_id"].as<String>();
  const uint64_t revision=data["revision"].as<uint64_t>();
  const bool on=data["on"].as<bool>();
  if (id.length()!=36 || revision==0) return false;
  portENTER_CRITICAL(&stateMux);
  const bool stale=revision<currentRevision || (on && revision<=closedRevision);
  portEXIT_CRITICAL(&stateMux);
  if (stale || (revision==currentRevision && commandID.length() && id!=commandID)) return false;
  const bool same=id==commandID;
  if (on && !same) {
    if (!storageHealthy) return false;
    const int64_t sampleUs=esp_timer_get_time();
    struct timeval wall; gettimeofday(&wall,nullptr);
    const int64_t epochUs=(int64_t)wall.tv_sec*1000000LL+wall.tv_usec;
    const int64_t candidate=uash::boundedDeadline(
      uash::deadlineForEnd(data["end_ms"].as<int64_t>(),epochUs,sampleUs),
      sampleUs,data["remaining_ms"].as<int64_t>());
    if (candidate==0) { stopOutputs(); return false; }
    const String session=data["session_id"].as<String>();
    const String execution=data["execution_id"].as<String>();
    if (session.length()!=36 || execution.length()!=36) return false;
    // Persist the new execution before GPIO can turn on. Failure stays OFF.
    xSemaphoreTake(persistenceMutex,portMAX_DELAY);
    stopOutputs();
    uash::RecoveryRecord next{};
    next.magic=uash::kRecordMagic; next.version=uash::kRecordVersion;
    session.toCharArray(next.session,sizeof(next.session));
    execution.toCharArray(next.execution,sizeof(next.execution));
    next.revision=revision; next.sequence=1;
    next.remainingMs=uash::remainingAt(candidate,esp_timer_get_time());
    next.completed=next.remainingMs==0;
    next.checksum=uash::recordChecksum(next);
    if (!uash::validRecord(next) || storage.putBytes("checkpoint",&next,sizeof(next))!=sizeof(next)) {
      storageHealthy=false;
      xSemaphoreGive(persistenceMutex);
      return false;
    }
    portENTER_CRITICAL(&stateMux);
    memcpy(&currentRecord,&next,sizeof(next));
    recordValid=true; recordDirty=false; tracking=true; deadlineUs=candidate; currentRevision=revision;
    portEXIT_CRITICAL(&stateMux);
    xSemaphoreGive(persistenceMutex);
  } else if (!on) {
    stopOutputs();
    portENTER_CRITICAL(&stateMux);
    currentRevision=revision;
    portEXIT_CRITICAL(&stateMux);
  }
  commandID=id;
  delay(30);
  portENTER_CRITICAL(&stateMux);
  const bool applied=outputOn==on && appliedRevision==revision;
  portEXIT_CRITICAL(&stateMux);
  if (!applied) return false;
  return true;
}

void contactBackend() {
  const bool starting=!bootstrapped;
  const String nonce=requestID();
  JsonDocument request;
  request["request_id"]=nonce;
  request["boot_id"]=bootID;
  uash::RecoveryRecord sample{};
  if (starting) {
    request["protocol"]=2;
    request["ready"]=true;
    if (snapshotRecord(sample)) appendCheckpoint(request["previous_session"].to<JsonObject>(),sample);
  } else if (snapshotRecord(sample)) appendCheckpoint(request["report"].to<JsonObject>(),sample);
  String body,response; serializeJson(request,body);
  const int status=postJSON(starting?"/controllers/bootstrap":"/controllers/poll",body,response);
  if (status==401 || status==403 || status==409) {
    stopOutputs(); bootstrapped=false;
    Serial.printf("Controlador no autorizado o arranque sin sincronizar (%d).\n",status);
    return;
  }
  if (status!=200) { Serial.printf("Sin respuesta valida (%d). Se conserva el corte local.\n",status); return; }
  JsonDocument doc;
  if (deserializeJson(doc,response)) return;
  const JsonObjectConst data=doc["data"].as<JsonObjectConst>();
  if (data["request_id"].as<String>()!=nonce || !applyCommand(data)) return;
  bootstrapped=true;
  JsonDocument ack;
  ack["boot_id"]=bootID;
  ack["command_id"]=commandID;
  ack["on"]=data["on"].as<bool>();
  if (snapshotRecord(sample)) appendCheckpoint(ack["report"].to<JsonObject>(),sample);
  body=""; response=""; serializeJson(ack,body);
  const int result=postJSON("/controllers/ack",body,response);
  if (result==401 || result==403 || result==409) { stopOutputs(); bootstrapped=false; }
  Serial.printf("Salidas: %s. Confirmacion: %d\n",data["on"].as<bool>()?"ENCENDIDAS":"APAGADAS",result);
}

void setup() {
  digitalWrite(HYDRO_PIN,LOW); digitalWrite(VACUUM_PIN,LOW);
  pinMode(HYDRO_PIN,OUTPUT); pinMode(VACUUM_PIN,OUTPUT);
  Serial.begin(115200);
  persistenceMutex=xSemaphoreCreateMutex();
  if (!persistenceMutex || xTaskCreate(outputTask,"uash-cutoff",2048,nullptr,2,nullptr)!=pdPASS) {
    Serial.println("No se pudo iniciar el control. Reiniciar placa.");
    while (true) delay(1000);
  }
  storageHealthy=storage.begin("uash-timer",false);
  if (storageHealthy && storage.getBytesLength("checkpoint")==sizeof(currentRecord)) {
    uash::RecoveryRecord previous{};
    if (storage.getBytes("checkpoint",&previous,sizeof(previous))==sizeof(previous) && uash::validRecord(previous)) {
      memcpy(&currentRecord,&previous,sizeof(previous)); recordValid=true;
    } else Serial.println("Registro de recuperacion invalido. El backend debe resolver la sesion.");
  }
  if (!storageHealthy || xTaskCreate(checkpointTask,"uash-checkpoint",3072,nullptr,1,nullptr)!=pdPASS) {
    storageHealthy=false;
    Serial.println("Almacenamiento no disponible. Salidas permanecen apagadas.");
    return;
  }
  bootID=requestID();
  configured=strlen(UASH_DEVICE_TOKEN)==64 && String(UASH_API_URL).startsWith("https://") &&
    strlen(WIFI_SSID) && strcmp(WIFI_SSID,"CONFIGURAR_WIFI")!=0;
  if (!configured) { Serial.println("Completar secrets.h antes de usar el controlador."); return; }
  WiFi.mode(WIFI_STA); WiFi.setAutoReconnect(true); WiFi.begin(WIFI_SSID,WIFI_PASSWORD);
  configTime(0,0,"pool.ntp.org","time.google.com");
  Serial.println("UASH protocolo 2: conectando y recuperando estado.");
}

void loop() {
  if (!configured || !storageHealthy) { delay(1000); return; }
  if (WiFi.status()!=WL_CONNECTED || time(nullptr)<1767225600) { delay(2500); return; }
  contactBackend();
  delay(2500);
}
