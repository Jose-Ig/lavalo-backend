# Checkout Pro con Orders API

Uash usa Checkout Pro con Orders API para reservas individuales en ARS. El backend calcula
250/1250/2500/5000 ARS para 3/15/30/60 minutos (tarifa actual). Los cálculos usan centavos enteros;
solo los importes enviados a Mercado Pago se convierten a strings decimales exactos.
Suscripciones, pruebas completas con la cuenta y salida a producción quedan fuera de esta etapa.

## Flujo

1. Expo crea la reserva y guarda su token de acceso en expo-secure-store.
2. POST /api/v1/reservations/:id/checkout, con el token como Bearer, calcula el precio y crea una order.
3. Go envía POST /v1/orders con type=online, processing_mode=manual, capture_mode=automatic,
   total_amount e items.unit_price como strings. Configura las URLs en config.online y recibe checkout_url.
4. Expo abre checkout_url con expo-web-browser. La URL HTTPS de retorno ofrece abrir uash://checkout/result.
5. GET /api/v1/reservations/:id/payment consulta GET /v1/orders/{order_id} y verifica el resultado.
6. POST /api/v1/payments/webhook verifica la firma HMAC del ID alfanumérico y consulta la order al proveedor.

El deep link nunca confirma una reserva. Solo processed/accredited con total_paid_amount igual al importe
esperado confirma un turno pendiente vigente. Se comprueban ID, referencia, moneda, vendedor y entorno.
created/processing/action_required no confirman. failed/canceled no equivalen a acreditación.
Los reembolsos parciales, estados desconocidos y pagos tardíos requieren revisión; no habilitan otra reserva.
Reembolsos totales y contracargos actualizan el estado; no se ejecutan devoluciones desde Uash.
La exclusión por espacio y la actualización de la reserva se hacen en una transacción.

## Idempotencia y vencimientos

Una order por reserva. Antes de llamar al proveedor se guardan UUID de idempotencia, referencia, vendedor,
entorno y cuerpo completo de la solicitud. Si se pierde la respuesta, se repite el mismo POST con la misma
X-Idempotency-Key y los mismos bytes, incluso después de reiniciar el proceso. Nunca se recalcula el cuerpo
ni se cambia de vendedor/entorno para recuperar un intento existente. Tras vencer la retención local,
no se repite la creación; las orders conocidas sí se siguen consultando.

La retención local es de hasta 10 minutos o hasta el inicio del turno. expiration_time usa el plazo restante
al preparar el intento; por ser relativo a la creación en Mercado Pago puede existir una diferencia por
latencia. La fecha local siempre es el límite estricto para confirmar. Pagos recibidos después requieren revisión.
Se excluyen ticket/ATM. La reconciliación consulta hasta 20 checkouts Orders por minuto, rotando también errores.

## Configuración de pruebas (manual, sin cambios automáticos en la cuenta)

En Integraciones, crear/seleccionar Checkout Pro con API integrada Orders. Usar el selector **Prueba**:

- MP_ENABLED=true (por defecto false).
- MP_LIVE_MODE=false.
- MP_ACCESS_TOKEN: Access Token del entorno Prueba; APP_USR también es válido para pruebas.
- MP_COLLECTOR_ID: ID del vendedor de prueba correspondiente al token (user_id de la order).
- MP_WEBHOOK_SECRET: firma secreta de los Webhooks de esa aplicación.
- MP_PUBLIC_BASE_URL: origen HTTPS público del backend, sin /api/v1 ni barra final.

Configurar Webhooks en modo Prueba para **Order (Mercado Pago)**, con URL
https://TU-DOMINIO/api/v1/payments/webhook. La guía específica envía topic/type=order;
no usar el antiguo evento payment ni confundir con merchant_orders. La firma incluye data.id en minúsculas.
No se envía notification_url en la creación de Orders; el webhook se configura en el panel.

Para comprar con Orders no hace falta una cuenta comprador de prueba: usar tarjetas de prueba, sin iniciar
sesión, desde incógnito. En mobile evitar la sesión personal del navegador. No compartir credenciales en el chat.
Go no carga .env automáticamente: exportar las variables antes de iniciar go run ./cmd/lavalo-api.
No colocar secretos en variables EXPO_PUBLIC_. Mantener separados entornos y bases de datos de prueba/producción.

## Mobile y compatibilidad

Los endpoints de Uash y checkout_url no cambian: Expo mantiene su contrato, token seguro e historial.
`Tengo una reserva` recupera el estado guardado. El estado se consulta al enfocar, volver al navegador,
reactivar la app y durante un minuto en primer plano. Los IDs de Orders quedan privados en Go.
El scheme y plugins requieren una development build para validar el retorno real en iOS/Android.
Usar Node >=20.19.4 con Expo SDK 54.

La migración es aditiva: conserva checkouts/recibos anteriores y la columna preference_id como histórico.
Un checkout sin api=orders se muestra en revisión y no se transforma ni se cobra de nuevo automáticamente.
Si existe una reserva de prueba anterior sin pagos, cancelarla y crear otra para probar Orders.
Si tuvo pagos reales, resolverla manualmente antes de cambiar de integración: no se consultan Payments/Preferences.

## Validación pendiente para el usuario

Paso 6: credenciales y webhook de prueba, compra aprobada/rechazada/pendiente, retorno en cada plataforma,
webhooks reales, pagos tardíos y recuperación tras cierre. Paso 7: revisión de costos y activación productiva.
La implementación solo ejecuta verificaciones locales con dobles HTTP; no crea recursos en la cuenta.

## Documentación oficial consultada

- https://www.mercadopago.com/developers/es/docs/checkout-pro-orders/create-order
- https://www.mercadopago.com/developers/es/docs/checkout-pro-orders/web-integration/configure-back-urls
- https://www.mercadopago.com/developers/es/docs/checkout-pro-orders/notifications
- https://www.mercadopago.com/developers/es/docs/checkout-pro-orders/payment-management/status/order-status
- https://www.mercadopago.com/developers/es/docs/checkout-pro-orders/additional-settings/define-order-validity
- https://www.mercadopago.com/developers/es/docs/checkout-pro-orders/integration-test-introduction

## Datos del comprador y resumen de tarjeta

Las nuevas reservas del formulario piden nombre, apellido, email, código de área y número local. Se guardan `first_name`, `last_name` y `phone_area_code` en `reservations`; `name` conserva el nombre completo y `phone` conserva el formato +54 para WhatsApp. La migración aditiva se aplica al iniciar el backend. Publicar backend antes de mobile.

Las nuevas Orders envían `payer.email`, `payer.first_name`, `payer.last_name` y `payer.phone` (área y número como strings). Por ejemplo: teléfono +543511234567 → `{"area_code":"351","number":"1234567"}`. No se deducen apellidos ni códigos de área de datos históricos. Si una reserva anterior no tiene email, se omite `payer`, porque Orders exige email cuando está presente. Las ampliaciones copian los datos del comprador del turno original.

`config.statement_descriptor` usa UASH por defecto. Se puede personalizar con `MP_STATEMENT_DESCRIPTOR` en Railway (máximo 13 caracteres). Los checkouts ya creados conservan su cuerpo original: los datos nuevos se aplican a nuevos intentos/reservas, sin alterar el UUID ni el cuerpo de los reintentos.

El flujo sigue siendo de invitado. No hay fecha de registro real y no se envía una fecha inventada ni `reservations.created_at` como sustituto. Este requisito del diagnóstico queda pendiente. La referencia de Orders documenta la clave literal `"payer.registration_date"` dentro de `additional_info`; no confundirla con un objeto `payer` anidado. Verificar este requisito con Mercado Pago antes de considerar completa la evaluación de integración.

Validación local: solicitudes serializadas, datos persistidos, códigos de área de 2/3/4 dígitos, compatibilidad histórica, copia a ampliaciones e idempotencia. No se ejecutan cobros reales ni se acredita el cumplimiento en el panel de Mercado Pago desde estas pruebas.

Fuentes oficiales (consultadas 2026-11-04):
- https://www.mercadopago.com.ar/developers/es/reference/online-payments/checkout-pro/create-order/post
- https://www.mercadopago.com.ar/developers/es/docs/checkout-pro-orders/additional-settings/configure-invoice-description
