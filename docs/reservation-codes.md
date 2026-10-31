# Reservas independientes, códigos y QR

Cada reserva tiene su propio ID y token de checkout. No existe un límite de una reserva por teléfono,
usuario o instalación. Solo se impide solapar reservas vigentes sobre el mismo espacio físico.
El almacenamiento local del checkout es una comodidad para continuar un pago, no la identidad del cliente.

Al confirmar el pago, dentro de la misma transacción se prepara un código único y estable por reserva,
con 80 bits de entropía derivados del secreto aleatorio de la reserva y su ID. Se almacena únicamente
el hash del código; nunca se incluye el código en URLs ni logs. La confirmación autenticada muestra
el código seleccionable en la app. Las reservas confirmadas anteriores lo obtienen al consultar el pago.
No se generan códigos de acceso válidos para reservas pendientes de pago o pagos en revisión.

POST /api/v1/reservations/lookup recibe {"code":"UASH-...."}. No necesita sesión del dispositivo.
Devuelve solo el turno, espacio y estado de habilitación; no expone token de pago, email ni teléfono.
POST /api/v1/reservations/verify-slot recibe {"code":"UASH-....","qr":"uash://slot/1"}.
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
Email se prepara solo si se indicó una dirección. WhatsApp se prepara solo con el consentimiento explícito
recogido en el formulario. Los intentos no guardan el código ni destinatario en texto: un futuro worker
los obtiene desde la reserva justo antes de enviar.

WhatsApp requiere WhatsApp Business Platform/Cloud API o proveedor, número habilitado, plantilla de
confirmación y consentimiento. El enlace wa.me de soporte abre un chat manual y no reemplaza esa API.
Email requiere SMTP o API transaccional y remitente configurado. SMS requiere un proveedor y costos
por país/destino: es una opción de respaldo a decidir, no está implementado.
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
Los envíos por WhatsApp/email quedan pendientes hasta elegir/configurar sus proveedores.
