# Reservas independientes, códigos y QR

Cada reserva tiene su propio ID y token de checkout. No existe un límite de una reserva por teléfono,
usuario o instalación. Solo se impide solapar reservas vigentes sobre el mismo espacio físico.
El almacenamiento local del checkout es una comodidad para continuar un pago, no la identidad del cliente.

Al confirmar el pago, dentro de la misma transacción se prepara un código único y estable por reserva,
de 6 dígitos derivado del secreto aleatorio de la reserva, su ID y un contador para resolver colisiones del día. Se almacena únicamente
el hash del código; nunca se incluye el código en URLs ni logs. La confirmación autenticada muestra
el código seleccionable en la app. Las reservas confirmadas anteriores lo obtienen al consultar el pago.
No se generan códigos de acceso válidos para reservas pendientes de pago o pagos en revisión.

POST /api/v1/reservations/lookup recibe {"code":"123456","date":"2026-10-31"}. No necesita sesión del dispositivo.
Devuelve solo el turno, espacio y estado de habilitación; no expone token de pago, email ni teléfono.
POST /api/v1/reservations/verify-slot recibe {"code":"123456","date":"2026-10-31","qr":"uash://slot/1"}.
Comprueba el código, pago confirmado, espacio exacto, disponibilidad y horario [inicio, fin).
Repetir un QR válido no duplica la verificación. No permite usar un código cancelado o fuera de horario.
Los endpoints limitan intentos a 60/minuto por conexión IP, con memoria acotada. Para despliegues con
varias instancias debe reemplazarse por un limitador distribuido y definir proxies confiables.

El QR estático identifica el espacio, no prueba presencia física ni enciende el equipo.
La respuesta awaiting_controller deja explícito que falta conectar el controlador del lavadero.
Antes de un comando físico real, la integración deberá verificar otra vez pago/horario, usar comandos
idempotentes y confirmar la respuesta del equipo. La app no muestra un servicio activo ni un temporizador ficticio.
Para probar la cámara se puede generar un QR con el texto exacto uash://slot/1 (cambiar el ID por el asignado).

## Avisos: pendientes de proveedor

notification_intents conserva una intención por reserva/canal, con restricción única para evitar duplicados.
Por ahora su estado es pending_configuration: NO hay dispatcher ni se envían mensajes automáticamente.
SMS se prepara por defecto para el celular de cada reserva confirmada. WhatsApp se agrega únicamente
con la casilla de consentimiento marcada; no reemplaza el SMS. El formulario solo solicita nombre y
celular: no solicita email ni datos del vehículo. Los campos históricos se conservan en la base.
Los intentos no guardan el código ni destinatario en texto: un futuro worker los obtiene desde la reserva.
SMS y WhatsApp requieren integrar y configurar proveedores de mensajería antes de realizar envíos reales.
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
6. La verificación termina en Espacio verificado, con activación física pendiente de integrar.
Los envíos por SMS/WhatsApp quedan pendientes hasta elegir/configurar sus proveedores.

## Pago por Mercado Pago

El botón Pagar crea el checkout de Mercado Pago y abre su URL. La reserva se confirma después
de verificar la aprobación con el proveedor. Se eliminó el modo de aprobación local automática.
Las reservas simuladas históricas se conservan, pero no se verifican como pagos de Mercado Pago:
crear una reserva nueva para probar el checkout. Las credenciales y MP_LIVE_MODE existentes se
conservan; abrir el proveedor no cambia automáticamente una integración de pruebas a producción.
Reiniciar Go cargando .env y recargar Expo antes de probar.

## Referencia y código corto

El número visible usa la fecha/hora del turno en Argentina: DDMMYYYY-HHmm (ejemplo 31102026-1430).
Es una referencia legible; el ID interno y el espacio distinguen reservas simultáneas.
Las reservas nuevas confirmadas reciben 6 números, incluidos ceros iniciales. Se almacenan solo
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
El código se conserva al cambiar de día salvo colisión con otro código de ese día, en cuyo caso
se genera otro y se muestra en la confirmación. El número visible refleja el nuevo horario.
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
