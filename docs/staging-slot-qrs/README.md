# QR de los cuatro espacios de staging

Estado: archivos generados y verificados; este directorio no implica que el SQL se haya aplicado en Railway.

Se conserva `slots.id` (1–4) como clave interna de las reservas. Se agrega `slots.public_id`,
un UUID v4 aleatorio y único. Cada QR contiene `uash://slot/<public_id>`.
El UUID identifica el espacio: la autorización sigue dependiendo del código de reserva,
la fecha, el pago confirmado, el horario y el espacio asignado. Un QR estático puede copiarse;
no constituye una prueba de presencia física.

## Primera instalación en staging

1. Conectarse a la base PostgreSQL de **staging** y ejecutar `asignar-staging.sql`
   **antes de desplegar por primera vez el backend con UUIDs**. Por ejemplo, desde la raíz del backend:
   ```sh
   psql "$STAGING_DATABASE_URL" -v ON_ERROR_STOP=1 -f docs/staging-slot-qrs/asignar-staging.sql
   ```
   La variable debe contener la conexión de staging accesible desde esa terminal; no guardar credenciales en Git.
   El script requiere que existan los espacios activos 1, 2, 3 y 4. No cambia sus IDs ni las reservas.
   Si ya tienen otro UUID, aborta: en ese caso exportar sus UUIDs reales a `slots.json` y regenerar los QRs.
   No sobrescribir UUIDs que ya estén en uso o impresos. El script es transaccional y puede repetirse con el mismo mapeo.
2. Comparar el resultado `SELECT id, label, public_id` con `slots.json`.
3. Desplegar el backend. Su migración preserva estos UUIDs y completa los de otros espacios,
   añade la restricción NOT NULL y el índice único. Los espacios nuevos reciben UUID automáticamente.
   Los QR anteriores `uash://slot/1` ya no se aceptan. La app no necesita cambios de formato.
4. Abrir `imprimir.html` en un navegador e imprimir en A4 al 100 %. También hay PNG y SVG individuales.
5. Con una reserva confirmada en su horario, consultar el código y la fecha en Uash y escanear el QR
   del espacio asignado. El de otro espacio debe rechazarse. El encendido físico sigue pendiente del controlador.

## Archivos y regeneración

- `slots.json`: mapeo estable entre IDs internos y UUIDs del juego de QRs preparado para staging.
- `asignar-staging.sql`: asociación inicial en PostgreSQL, pendiente de aplicar.
- `espacio-1.png` a `espacio-4.png`: códigos con margen blanco de cuatro módulos y corrección M.
- `espacio-1.svg` a `espacio-4.svg`: versiones vectoriales.
- `imprimir.html`: hoja de cuatro carteles identificados como STAGING.

Para regenerar imágenes a partir del mismo manifiesto, sin rotar IDs ni tocar la DB:

```sh
python3 -m venv /tmp/uash-qr-tools
/tmp/uash-qr-tools/bin/python -m pip install 'qrcode[pil]==8.2'
/tmp/uash-qr-tools/bin/python tools/generate_slot_qrs.py docs/staging-slot-qrs/slots.json
```

En producción usar sus propios UUIDs y carteles; no ejecutar este SQL de staging.
