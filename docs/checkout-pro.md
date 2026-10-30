# Checkout Pro: contrato de implementación

Alcance: reservas individuales en ARS, Checkout Pro con preferencias. Suscripciones fuera de alcance.
El backend calcula 1250/2500/5000 ARS para 15/30/60 minutos (tarifa actual de la app).
Los cálculos internos usan centavos enteros. El importe mostrado antes de crear el checkout es estimativo.

Una reserva pendiente retiene el espacio hasta expires_at (actualmente 10 minutos o inicio del turno).
Una preferencia por reserva, reutilizada en reintentos y dobles toques; su vencimiento coincide con el turno retenido.
Una respuesta ambigua del proveedor nunca autoriza crear otra preferencia automáticamente.
No se acepta dinero real por defecto: MP_ENABLED=false; MP_LIVE_MODE=false.

POST /api/v1/reservations/:id/checkout y GET /api/v1/reservations/:id/payment requieren el token de reserva como Bearer.
GET consulta y reconcilia el estado con Mercado Pago. Nunca se acepta un estado de pago enviado por el celular.
POST /api/v1/payments/webhook requiere firma HMAC válida y vuelve a consultar el pago al proveedor.
El retorno HTTPS /api/v1/payments/return abre uash://checkout/result sin incluir tokens privados.

Solo un pago aprobado, con importe, moneda, vendedor y referencia correctos, confirma una reserva pendiente vigente.
La confirmación usa la misma exclusión por espacio que las reservas para evitar sobreventas.
Pagos aprobados tardíos, duplicados o posteriores a cancelación requieren revisión; no confirman otro turno.
Devoluciones y contracargos actualizan el pago y bloquean la confirmación; no se ejecutan devoluciones automáticas.
Cancelar una reserva pagada requiere una política de devolución y sigue bloqueado.

La app conserva el token en almacenamiento seguro, abre el navegador nativo, y consulta al volver o recuperar la sesión.
El deep link solo navega. Pendiente/rechazado/cerrado no equivalen a reserva confirmada.

Pasos 1–5: reglas, persistencia, backend, confirmación/reconciliación, Expo.
Paso 6 (prueba completa con Mercado Pago) y paso 7 (producción) quedan a cargo del usuario.
No se crean aplicaciones, credenciales, usuarios ni webhooks en la cuenta durante la implementación.

Fuentes: https://www.mercadopago.com/developers/es/docs/checkout-pro-preferences/create-payment-preference
https://www.mercadopago.com/developers/es/docs/checkout-pro-preferences/mobile-integration/react-native-expo-go

## Operación del backend
Se usan endpoints REST oficiales mediante net/http, con timeout, sin reintentos automáticos de POST.
Si la respuesta de creación se pierde, el intento permanece `creating`: consultar Mercado Pago por su referencia
antes de cualquier reparación manual. No borrar el intento y volver a cobrar sin verificarlo.
La reconciliación consulta pagos cada minuto (20 intentos por ciclo, ordenados por última consulta).
Una respuesta `verified=false` nunca habilita la pantalla de éxito; GET payment reconcilia antes de responder.
`review` requiere intervención del negocio; no se ejecutan devoluciones ni se confirman reservas tardías.
Para reducir pagos tardíos se excluyen ticket/ATM; medios asíncronos restantes siguen sujetos a revisión.
