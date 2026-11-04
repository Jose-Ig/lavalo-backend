# Reservas independientes, códigos y QR

Cada reserva tiene su propio ID y token de checkout. No existe un límite de una reserva por teléfono,
usuario o instalación. Solo se impide solapar reservas vigentes sobre el mismo espacio físico.
El almacenamiento local del checkout es una comodidad para continuar un pago, no la identidad del cliente.

Al confirmar el pago, dentro de la misma transacción se prepara un código único y estable por reserva,
de 6 dígitos. El ID público visible es `código-ddmmaa` (ejemplo `123456-311026`); el código para consulta, QR y futuros mensajes de WhatsApp/email sigue siendo solo `123456`. Los dígitos se derivan del secreto aleatorio de la reserva, su ID y un contador para resolver colisiones del día. Se almacena únicamente
el hash del código; nunca se incluye el código en URLs ni logs. La confirmación autenticada muestra
el código seleccionable en la app. Las reservas confirmadas anteriores lo obtienen al consultar el pago.
No se generan códigos de acceso válidos para reservas pendientes de pago o pagos en revisión.

POST /api/v1/reservations/lookup recibe {"code":"123456","date":"2026-10-31"}. No necesita sesión del dispositivo.
Devuelve solo el turno, espacio y estado de habilitación; no expone token de pago, email ni teléfono.
POST /api/v1/reservations/verify-slot recibe {"code":"123456","date":"2026-10-31","qr":"uash://slot/<public_id UUID>"}.
Comprueba el código, pago confirmado, espacio exacto, disponibilidad y horario [inicio, fin).
Repetir un QR válido no duplica la verificación. No permite usar un código cancelado o fuera de horario.
Los endpoints limitan intentos a 60/minuto por conexión IP, con memoria acotada. Para despliegues con
varias instancias debe reemplazarse por un limitador distribuido y definir proxies confiables.

El QR estático identifica el espacio, no prueba presencia física ni enciende el equipo.
Para probar el flujo sin Arduino, un QR válido devuelve `activation_status: simulated`. El check-in queda guardado en `qr_verified_at` y habilita la cuenta regresiva y la compra de tiempo sin esperar al controlador. No se envían comandos físicos.
Antes de un comando físico real, la integración deberá verificar otra vez pago/horario, usar comandos
idempotentes y confirmar la respuesta del equipo. La app muestra una cuenta regresiva hasta `end_time` después de validar el QR, sin afirmar que el equipo se encendió.
Los espacios conservan su ID numérico interno y tienen un `public_id` UUID v4 único y estable,
asignado automáticamente al crearlos o migrar los existentes. El QR usa `uash://slot/<public_id>`;
los identificadores numéricos del formato anterior se rechazan. El reinicio no rota los UUIDs.
Los [cuatro carteles de staging y su instalación](staging-slot-qrs/README.md) incluyen el SQL
que debe aplicarse antes del primer despliegue para asociar estos archivos a los espacios 1–4.

## Avisos: pendientes de proveedor

notification_intents conserva una intención por reserva/canal, con restricción única para evitar duplicados.
Por ahora su estado es pending_configuration: NO hay dispatcher ni se envían mensajes automáticamente.
WhatsApp se prepara por defecto para el celular de cada reserva confirmada. No se solicita una casilla.
El formulario informa que el código se enviará por WhatsApp. Email es opcional y se suma como canal
si se informa una dirección válida. No se generan nuevos avisos SMS y los SMS pendientes de la reserva
consultada se marcan cancelled_channel_disabled. Los registros históricos se conservan.
El formulario solicita nombre, celular y email opcional, sin datos del vehículo.
Los intentos no guardan código ni destinatario en texto: un futuro worker los obtiene desde la reserva.
WhatsApp y email requieren integrar sus proveedores antes de realizar envíos reales.
No se deben marcar avisos como entregados hasta tener confirmación del proveedor.
Los errores de mensajería no deben revertir un pago aprobado ni generar un código distinto al reintentar.

Fuentes consultadas:
https://whatsappbusiness.com/products/business-platform-features/
https://www.twilio.com/docs/messaging/api

## Prueba local

Reiniciar Go aplica las migraciones aditivas (code_hash, qr_verified_at, consentimiento y notification_intents).
Reiniciar Metro con npx expo start --clear. Una development build anterior necesita recompilarse para expo-camera.
1. Confirmar una reserva de prueba y guardar el código de la pantalla de pago.
2. Volver a Inicio y crear otra reserva en otro turno: tiene otro código.
3. Tengo una reserva siempre solicita un código; no consulta automáticamente el checkout guardado.
4. Consultar cualquiera de los códigos, incluso desde otro dispositivo conectado al mismo backend.
5. Dentro del horario reservado, escanear el QR del espacio correcto; uno distinto debe rechazarse.
6. El escaneo abre una modal desde Tengo una reserva; al validarse se oculta Editar reserva y aparece la cuenta regresiva en negro. Se muestra “Modo de prueba: equipo simulado.”; no requiere un Arduino conectado.
Los envíos por WhatsApp/email quedan pendientes hasta elegir/configurar sus proveedores.

## Pago por Mercado Pago

El botón Pagar crea el checkout de Mercado Pago y abre su URL. La reserva se confirma después
de verificar la aprobación con el proveedor. Se eliminó el modo de aprobación local automática.
Las reservas simuladas históricas se conservan, pero no se verifican como pagos de Mercado Pago:
crear una reserva nueva para probar el checkout. Las credenciales y MP_LIVE_MODE existentes se
conservan; abrir el proveedor no cambia automáticamente una integración de pruebas a producción.
Reiniciar Go cargando .env y recargar Expo antes de probar.

## Referencia y código corto

El ID/número visible usa el código de reserva y la fecha local del turno en Argentina: `123456-311026` (`código-ddmmaa`). Antes de confirmar el pago queda vacío para no revelar el código de acceso.
Es una referencia legible; el ID interno y el espacio distinguen reservas simultáneas.
Las reservas confirmadas de versión 2 tienen un código de seis números, incluidos ceros iniciales, sin sufijo de fecha. La app solicita ese código y la fecha por separado. Los IDs con fecha emitidos anteriormente siguen aceptándose como compatibilidad, pero las respuestas y los futuros avisos usan el código de seis números. Se almacenan solo
su hash con la fecha del turno y la versión/contador de derivación. Los códigos no se duplican
para un mismo día; ante colisión se usa otro candidato y el índice único impide asignación duplicada.
Una colisión concurrente revierte la transacción y admite reintento. Los códigos antiguos se conservan.
Consulta y QR reciben date (AAAA-MM-DD); si se omite, se usa hoy en Argentina. Nunca se busca un
código corto en otros días. La app solicita la fecha y la conserva al escanear. El límite de intentos
sigue vigente; para producción se requiere el limitador distribuido mencionado arriba.

## Cambio de turno

Una reserva confirmada se puede reprogramar antes de su inicio y sin QR validado. Se conserva
la duración pagada, ID, contacto y checkout; la fecha/hora y el espacio se actualizan en una transacción.
La disponibilidad excluye la propia reserva. La escritura bloquea los espacios y comprueba de nuevo
los solapamientos. El turno anterior solo se libera si el cambio se guarda correctamente.
Al cambiar de día se actualiza el sufijo `ddmmaa` del ID público; los seis dígitos del código de acceso se conservan salvo colisión con otro código de ese día, en cuyo caso se generan otros y se muestran en la confirmación. El número visible refleja el nuevo horario.
Consulta y QR deben usar la nueva fecha; el día anterior deja de aceptar el código corto.
Los avisos siguen pendientes de proveedor; no se prometen SMS/WhatsApp de reprogramación reales.

## Asignación automática de espacio

La app pide solo día, horario, duración y contacto. No envía slot_id al crear o reprogramar.
El backend asigna un espacio habilitado libre durante todo el intervalo, dentro de la misma
transacción que guarda la reserva. Bloquea los espacios en orden estable y vuelve a comprobar
solapamientos, incluyendo retenciones pendientes vigentes. Si no queda lugar devuelve conflicto.
En cambios de turno conserva el espacio actual si está disponible; de lo contrario asigna otro.
El pago y la duración no cambian. El espacio definitivo aparece en la reserva guardada y se valida
contra el QR del espacio asignado. La selección de espacio queda solo para clientes API anteriores.

## Compra de tiempo adicional

Después de validar el QR, mientras el turno esté confirmado y vigente, se pueden comprar 15, 30 o 60 minutos adicionales sobre el mismo espacio. La cuenta regresiva usa el fin programado y continúa al volver a la app. El detalle muestra el número de reserva; el código se conserva en el campo de consulta.

- `POST /api/v1/reservations/extensions/availability`: `{ "code": "123456", "date": "2026-10-31" }`; devuelve opciones `{ minutes, amount_cents }` con tarifa del servidor.
- `POST /api/v1/reservations/extensions`: mismo body más `minutes`; devuelve `{ data, access_token }` para pagar mediante el checkout Orders existente.

La compra crea una reserva hija pendiente (`parent_reservation_id`, columna nullable agregada por AutoMigrate). Retiene únicamente el intervalo adicional durante un máximo de 10 minutos, limitado por el final del turno original. Incluye reservas confirmadas y retenciones vigentes al comprobar solapamientos; respeta el cierre de las 21:00. Cancelar o vencer la ampliación no cancela la reserva original.

La confirmación de pago bloquea el espacio, la reserva original y la ampliación; comprueba de nuevo pago, vigencia, espacio habilitado y continuidad. Solo entonces amplía `end_time` y la duración de la original; conserva su código y QR. La hija pasa a `completed` para no duplicar ocupación. Los reintentos no suman minutos otra vez. Pagos tardíos/conflictos y devoluciones posteriores se dejan en revisión; no se recorta silenciosamente un servicio en curso.

En mobile se abre Pago para la ampliación y se vuelve a Tengo una reserva. Se reconcilia el checkout actual solo si corresponde a la reserva consultada explícitamente, y se vuelve a consultar el fin en backend. No se recuperan reservas ajenas ni se simulan aprobaciones.

Las etiquetas numéricas de espacios se muestran como `Espacio N° n`, incluso cuando una base existente conserva `Espacio n`. Este cambio no modifica los IDs internos ni los UUIDs/QR de espacios.

## Simulación de controlador para pruebas

El escaneo sigue validando código, fecha, pago confirmado, espacio habilitado y horario `[inicio, fin)`. Solo después registra el check-in y devuelve `simulated`, también en consultas posteriores. La cuenta regresiva usa `end_time` del servidor; volver a escanear no reinicia el tiempo. Se oculta Editar reserva y se habilita Agregar más tiempo durante el turno vigente.

La disponibilidad y los pagos adicionales siguen usando el flujo real de reservas y Mercado Pago. Solo la respuesta del Arduino se simula: no se aprueban pagos automáticamente, no se agregan minutos gratis y no se modifica el horario original al escanear. Al confirmarse un pago adicional se actualiza el fin de la reserva y la app refresca la cuenta regresiva.

## MVP: controlador HTTPS por espacio

El control real del prototipo se documenta en `../firmware/README.md`. Un espacio registrado en `devices` deja de considerar el QR como confirmación del equipo: devuelve `awaiting_controller`, `controller_offline` o `controller_unavailable` hasta que exista una confirmación reciente del comando correcto; entonces devuelve `active`. Los espacios no registrados conservan la simulación. No se cambia ninguna credencial de pago.

El ESP32 consulta POST `/api/v1/controllers/poll` con `request_id` y `boot_id` (UUID), usando su token privado en Authorization. Recibe sólo orden, versión, hora del servidor y hora de corte; no datos personales ni código del cliente. Devuelve POST `/api/v1/controllers/ack` con `command_id` y `on`. La orden es estable mientras la reserva y su fin no cambien. Un arranque nuevo invalida las confirmaciones anteriores. La compra de tiempo sólo cambia el comando después de confirmar el pago.

Las consultas y confirmaciones bloquean espacio y dispositivo en ese orden. El dispositivo corta localmente con un temporizador monotónico separado del hilo de red. La hora de corte no se renueva por repetir consultas. HTTP usa límites de cuerpo y de frecuencia, autenticación individual y TLS validado en firmware.
