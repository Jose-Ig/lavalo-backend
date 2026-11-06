# Recuperación del tiempo del turno — protocolo 2

El Checkout existente sigue cobrando el turno y sus ampliaciones. Esta funcionalidad administra solamente tiempo y estado operativo. No calcula ni procesa reintegros.

## Comportamiento

- Sin comunicación durante más de 12 segundos: el backend conserva la última muestra válida y la app congela el contador. Un monitor independiente detecta esto aun con la app cerrada.
- Wi-Fi vuelve sin reinicio: se mantiene el vencimiento operativo que ya tenía el ESP32; la app muestra el tiempo actualizado o finalización. Los equipos pueden haber seguido habilitados durante la desconexión.
- Reinicio: GPIO apagados; consulta de arranque autenticada y conciliación con el registro NVS. Si hay tiempo pendiente y el intervalo está libre, se autoriza una nueva ejecución con ese tiempo. Si no hay sesión pendiente, permanece apagado.
- Un turno finalizado o cerrado nunca se recupera por un QR repetido o una notificación de pago.
- Un intervalo de recuperación que invade otra reserva queda `pending_recovery`, con tiempo conservado y salida apagada. También se bloquean registros faltantes/incoherentes y espacios deshabilitados.

Una pérdida de Wi-Fi y un corte eléctrico no se distinguen físicamente por red. El identificador de arranque distingue continuidad del proceso frente a reinicio; también un reinicio por software puede recuperar tiempo.

## Modelos

`sessions` guarda el estado operativo y tiempo pendiente por reserva. `executions` registra las autorizaciones y sus vencimientos independientes del horario comercial. `incidents` conserva las muestras de interrupción. `boots` registra los arranques y rechaza procesos retirados. `devices.session_id` mantiene la última sesión aunque pasó el horario comercial.

Las migraciones se ejecutan al iniciar la API o con `go run ./cmd/lavalo-api --migrate-only`. No es necesario borrar los datos de staging para agregar estas tablas. Las ejecuciones recuperadas participan de disponibilidad, asignación y validación de ampliaciones. Las compras de minutos se adjuntan una sola vez; ninguna notificación vuelve a activar una sesión terminal.

## API del controlador

Todas las rutas son POST bajo `/api/v1`, con HTTPS y `Authorization: Bearer <token individual>`. Las respuestas no incluyen datos del cliente ni credenciales de pago.

### `/controllers/bootstrap`

```json
{
  "request_id": "UUID v4",
  "boot_id": "UUID nuevo por encendido, estable durante sus reintentos",
  "protocol": 2,
  "ready": true,
  "previous_session": {
    "session_id": "UUID de sesión",
    "execution_id": "UUID de ejecución persistida",
    "revision": 7,
    "sequence": 42,
    "remaining_ms": 900000,
    "completed": false
  }
}
```

`previous_session` se omite si no hay registro. Una sesión previa conocida por el servidor pero sin registro local válido queda pendiente de revisión. `ready: false` permite registrar/preparar el arranque sin autorizar su recuperación; la versión de firmware incluida envía `true` después de conectarse y sincronizar su reloj.

La respuesta usa `data` e incluye `request_id`, `boot_id`, `protocol`, `command_id`, `revision`, `on`, `server_ms`, `end_ms`, `remaining_ms`, `session_id`, `execution_id`, `state`, `reason` y `poll_ms`. Un `on: true` es la autorización explícita; el tiempo restante aislado no autoriza GPIO.

### `/controllers/poll`

Envía `request_id`, el `boot_id` ya registrado y `report` con el mismo formato que `previous_session`. Al reconectar dentro del mismo arranque no llama a bootstrap ni reinicia el contador. Una respuesta repetida conserva su comando y vencimiento.

### `/controllers/ack`

Envía `boot_id`, `command_id`, `on` y `report` de la ejecución aplicada. Devuelve 204. El ACK describe GPIO; no demuestra tensión ni funcionamiento de la máquina.

Un firmware anterior que omita bootstrap recibe 409 y no puede habilitar equipos. Un arranque retirado tampoco puede recuperarse. Una autorización persistida antes de encender permite reconciliar respuestas y ACK perdidos sin duplicar tiempo. Los reintentos del mismo arranque no vuelven a calcular el plazo.

## App

Las consultas de reserva incluyen `operational_state`, `operational_reason`, `remaining_ms`, `operational_end_ms`, `server_ms`, `operational_version` y `timer_frozen`. La app usa el vencimiento operativo y una corrección del reloj local estimada con la respuesta del servidor. Si pierde conexión solamente el teléfono, congela su visualización provisionalmente y vuelve a consultar al recuperar conexión.

## Persistencia del ESP32 y precisión

El firmware utiliza Preferences/NVS con un único registro versionado y checksum. Persiste una autorización nueva antes de habilitar GPIO y registra puntos de tiempo cada 5 segundos durante la ejecución, además de la finalización. El corte monotónico corre en otra tarea independiente de HTTPS y de las escrituras.

El instante exacto del corte no se mide. La última muestra del servidor limita el tiempo restaurado; el registro local permite descontar minutos utilizados offline. En una pérdida abrupta puede recuperarse aproximadamente hasta un intervalo de checkpoint que aún no llegó a guardarse; una escritura interrumpida y la temporización real de la placa deben ensayarse físicamente. La frecuencia de escritura y el desgaste de flash deben revisarse según el uso real.

Si el registro está dañado, no se enciende con un tiempo inventado. Si hay bloqueo de disponibilidad, el tiempo queda conservado para soporte. No hay un panel administrativo nuevo en esta entrega. Estos casos requieren resolución operativa antes de habilitar la sesión; no se sustituyen por devoluciones automáticas.

## Despliegue de staging

1. Desplegar backend y ejecutar las migraciones de arranque. Mantener la configuración actual de Checkout.
2. Actualizar la app con los campos operativos y la pantalla de contador congelado.
3. Cargar el firmware de protocolo 2, conservando token, Wi-Fi y CA en `secrets.h` privado. El registro NVS se crea al usar una sesión nueva. No cargar un binario compilado con los valores de ejemplo como si estuviera configurado.
4. Comprobar primero las dos salidas LED del prototipo. El código no implementa una etapa de potencia ni una función de seguridad certificada; una parada de emergencia física permanece independiente de la recuperación.
5. Probar un turno: Wi-Fi perdido y recuperado, energía perdida y recuperada, desconexión hasta terminar, falla combinada Wi-Fi→energía y conflicto con reserva siguiente.

## Pruebas reproducibles

```sh
go test ./...
go test -race ./internal/controllers ./internal/bookings ./internal/payments
c++ -std=c++11 firmware/tests/deadline_test.cpp -o /tmp/uash-deadline-test
/tmp/uash-deadline-test
c++ -std=c++11 firmware/tests/recovery_test.cpp -o /tmp/uash-recovery-test
/tmp/uash-recovery-test
```

En el repositorio mobile:

```sh
node node_modules/typescript/bin/tsc --noEmit
node --test tests/reservationTimer.test.cjs
```

Compilar el sketch con Arduino ESP32 3.3.12 y ArduinoJson 7.4.2, tal como el prototipo anterior. La compilación no reemplaza el ensayo de corte eléctrico con la placa real.
