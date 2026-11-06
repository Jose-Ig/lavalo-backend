# Uash: controlador HTTPS con recuperación de tiempo (protocolo 2)

La especificación vigente, el despliegue y las pruebas están en [recuperación del controlador](../docs/controller-recovery.md). Backend, app y firmware deben actualizarse juntos.

La placa consulta directamente al backend cada 2,5 segundos, más el tiempo de cada petición. No requiere broker MQTT ni otra cuenta. Este paquete es para el circuito de dos LED ya probado: P25 y P26, una resistencia de 1 kΩ por LED y GND compartido. No conectar máquinas ni tomas de red a la protoboard.

## Qué ya está preparado y qué falta

El backend incorpora `/api/v1/controllers/bootstrap`, `/api/v1/controllers/poll` y `/api/v1/controllers/ack`, todos POST y autenticados con `Authorization: Bearer <token del dispositivo>`. Los cambios todavía deben desplegarse. El ESP32 arranca apagado y sólo enciende tras una autorización HTTPS válida para una reserva confirmada, con QR validado, espacio habilitado y horario vigente. No hay un botón público de encendido manual.

La app muestra Activando hasta recibir una confirmación reciente. El reconocimiento sólo confirma las salidas GPIO; no mide tensión en el toma ni confirma que un motor funcione. Si pierde comunicación, la app deja de afirmar que el equipo está activo. El ESP32 conserva la hora de corte ya autorizada aunque falle Internet; no necesita la app abierta ni la notebook encendida.

## Paso 1: publicar el backend y actualizar la app

Desplegar los cambios del backend. El arranque aplica AutoMigrate y crea/actualiza `devices`, `sessions`, `executions`, `incidents` y `boots` (no cambia UUIDs de espacios). Actualizar la app con los nuevos estados del controlador. No cambiar credenciales de Mercado Pago.

Los espacios sin un controlador registrado conservan el modo de simulación anterior. Los que tienen controlador registrado esperan una confirmación real de la placa. Registrar una placa mientras exista un turno ya escaneado y vigente puede habilitar sus LED en cuanto consulte: realizar el registro con el espacio libre.

## Paso 2: identificar el espacio real de staging

En la consola de la base PostgreSQL de staging, consultar:

```sql
SELECT id, label, public_id FROM slots ORDER BY id;
```

Elegir el espacio del primer prototipo y copiar su UUID real. El QR impreso debe contener `uash://slot/<ese UUID>`. No usar automáticamente el manifiesto antiguo de carteles: puede diferir de la base real. No sobrescribir UUIDs existentes.

## Paso 3: registrar el dispositivo sin compartir secretos

Este paso lo realiza quien administra la base. Hay dos alternativas; usar sólo una.

### A. Generación local y SQL para PostgreSQL remoto

El ZIP incluye `provision_controller.py`, que usa Python 3 estándar y no se conecta a Internet ni a la base. Desde una terminal, en la carpeta del archivo:

```text
python provision_controller.py --slot-public-id UUID_REAL --output-dir controller-private
```

En Windows también puede usarse `py` en lugar de `python`.

Se crea una carpeta privada nueva con:

- `secrets.h`: credencial aleatoria del dispositivo. No enviarla por chat ni subirla a Git.
- `register-controller.sql`: registro con el hash de esa credencial, para aplicar en la consola PostgreSQL correcta después del despliegue.

No se imprime la credencial. El SQL falla si el UUID no existe. Si ya había un controlador para ese espacio, rota su credencial e invalida la anterior; no crea dos controladores activos para el mismo espacio. En Windows los permisos finales dependen también de la configuración de la cuenta y la carpeta.

Después de aplicar el SQL, copiar `secrets.h` dentro de `UashController`, junto al archivo `.ino`. Completar allí `WIFI_SSID` y `WIFI_PASSWORD` con los datos locales. Mantener el token generado. El servidor usa el hash; el ESP32 usa el token original.

### B. Comando Go con acceso administrativo a la misma base

Desde el repositorio backend, con las variables de la base correcta cargadas localmente:

```text
go run ./cmd/lavalo-api --controller-slot UUID_REAL --controller-file /ruta/privada/controller-credentials.json
```

El archivo debe ser nuevo: el comando no sobrescribe uno existente. Guarda token e identificación sólo en ese archivo con permisos 0600. No cambia variables ni credenciales del proveedor. Copiar el token a un `secrets.h` creado a partir de `secrets.example.h`; hacerlo localmente, sin enviarlo por chat. Este comando también rota el token si ese espacio ya tiene un controlador.

## Paso 4: abrir el programa en Arduino IDE

1. Descomprimir el ZIP.
2. Abrir `UashController/UashController.ino`.
3. Instalar desde Library Manager la biblioteca **ArduinoJson**, autor **Benoit Blanchon**, versión **7.4.2** (la usada en la compilación verificada).
4. Completar `secrets.h` siguiendo el paso anterior. Si no está configurado, el firmware mantiene apagadas las salidas y avisa por el Monitor serie.
5. Mantener `trusted_ca.h` junto al sketch. Contiene ISRG Root X2, verificada contra el certificado de staging el 04/11/2026. El firmware verifica el certificado y el nombre del servidor; no usa `setInsecure`. Si el proveedor cambia de CA habrá que actualizar ese archivo de forma verificada.
6. Seleccionar **ESP32 Dev Module**, el puerto USB correcto y cargar con Upload.
7. Abrir Serial Monitor a **115200 baud**. La placa sincroniza su reloj por NTP antes de usar HTTPS.

No hace falta cambiar la instalación eléctrica del circuito de LED. Para cambiar cables, desconectar primero el USB. La placa necesita Wi-Fi de 2,4 GHz con acceso a Internet y NTP.

## Paso 5: prueba completa con una reserva

1. Usar una reserva nueva de staging que haya sido confirmada realmente por Mercado Pago.
2. Consultar su código y fecha en Tengo una reserva.
3. Dentro del horario reservado, escanear el QR del espacio asignado y registrado.
4. Ver Activando en la app. Tras la consulta y confirmación de la placa, ambas luces deben encenderse y aparecer el tiempo restante.
5. El Monitor serie informa `Salidas: ENCENDIDAS. Confirmacion: 204`. El código 204 es la confirmación HTTP del backend.
6. Comprar tiempo adicional sólo si hay disponibilidad. El fin cambia cuando Mercado Pago confirma el pago. La placa recibe una nueva versión de la autorización y actualiza el corte; la app espera la nueva confirmación.
7. Al terminar el horario autorizado, ambas luces se apagan por un temporizador local independiente de las peticiones de red.

Al activar inicialmente, se usa el final programado de la reserva; escanear tarde no entrega una duración nueva completa. Después de un reinicio se puede autorizar un vencimiento operativo nuevo para el tiempo pendiente, si no invade otra reserva. Volver a escanear o consultar no reinicia el temporizador. Una compra pendiente no agrega minutos. Las respuestas no incluyen código de reserva, nombre, teléfono ni email.

## Pruebas de fallo

- Cerrar la app: las luces continúan hasta el corte autorizado.
- Cortar Internet durante un turno: la placa conserva el corte previamente autorizado. Una ampliación todavía no recibida no puede aplicarse offline.
- Reiniciar la placa: las salidas arrancan apagadas; consulta bootstrap con su registro persistido. Recupera el tiempo pendiente cuando el backend autoriza la continuación. Los arranques anteriores quedan invalidados.
- QR de otro espacio, turno fuera de horario o pago pendiente: no habilitan.
- Token inválido o dispositivo deshabilitado: una respuesta 401/403 apaga las salidas. Si la placa está offline, la revocación sólo llega al reconectar; conserva el corte local anterior.
- Si el firmware no conecta: revisar el Monitor serie. 404 puede indicar que aún no se desplegaron las rutas; 401 indica un problema de credencial/registro; 409 indica una orden que cambió y obliga a consultar nuevamente. No enviar `secrets.h` ni tokens al pedir ayuda.

## Alcance de las verificaciones realizadas

Suite Go con SQLite y proveedor de pagos falso de tests: autenticación, rotación, aislamiento de espacios, QR, vencimientos, repetición de órdenes, reinicios, API, rechazo de estados indebidos y ampliación pagada. TypeScript verificado. Firmware compilado para `esp32:esp32:esp32` con Arduino ESP32 3.3.12 y ArduinoJson 7.4.2. No se cargó este firmware en una placa, no se aplicó SQL a Railway y no se verificó una compra física de extremo a extremo. Las pruebas del corte durante una petición bloqueada deben completarse en el prototipo.

Este MVP prueba lógica de control con LED. La etapa de potencia del producto final requiere el diseño eléctrico y la instalación correspondientes.

## Referencias de implementación

- https://docs.espressif.com/projects/arduino-esp32/en/latest/api/wifi.html
- https://github.com/espressif/arduino-esp32/tree/master/libraries/NetworkClientSecure
- https://github.com/espressif/arduino-esp32/tree/master/libraries/HTTPClient
- https://arduinojson.org/v7/api/json/deserializejson/

## Corrección del adelanto de corte

El firmware usa la fecha UTC `end_ms` y una muestra NTP de `gettimeofday()` tomada al recibir una autorización nueva. Convierte ese fin a un plazo monotónico fijo. No descuenta el establecimiento de HTTPS y no conserva el mínimo de las consultas sucesivas. Las repeticiones no adelantan ni reinician el plazo; una autorización nueva por compra pagada sí puede modificarlo. El temporizador independiente sigue cortando sin red.

Prueba reproducible del cálculo, sin placa:

```sh
c++ -std=c++11 firmware/tests/deadline_test.cpp -o /tmp/uash-deadline-test
/tmp/uash-deadline-test
```

La app corrige su referencia temporal con el servidor y el ESP32 usa NTP para convertir el vencimiento operativo a un plazo monotónico. La latencia y la precisión de los registros persistidos siguen siendo relevantes. No se agrega una tolerancia fija de segundos al fin de la reserva.
