"""Generate private firmware configuration and PostgreSQL registration SQL offline.
No network calls and no secrets are printed. SQL must be applied by a database administrator.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import uuid
from urllib.parse import urlsplit

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--slot-public-id', required=True)
    parser.add_argument('--output-dir', required=True, type=Path)
    parser.add_argument('--api-url', default='https://uash-api-staging.up.railway.app/api/v1')
    args = parser.parse_args()
    slot = uuid.UUID(args.slot_public_id)
    if slot.version != 4 or str(slot) != args.slot_public_id:
        parser.error('Use the actual canonical slot public UUID v4 from the database')
    url = urlsplit(args.api_url)
    if url.scheme != 'https' or not url.hostname or url.username or url.query or url.fragment:
        parser.error('Use an HTTPS API URL without credentials or query parameters')
    args.output_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    token = secrets.token_hex(32)
    device_id = str(uuid.uuid4())
    token_hash = hashlib.sha256(token.encode()).hexdigest()
    config = ('#pragma once\n'
        '// PRIVATE: do not commit, upload, screenshot, or send this file.\n'
        'const char* WIFI_SSID = "CONFIGURAR_WIFI";\n'
        'const char* WIFI_PASSWORD = "CONFIGURAR_CLAVE_WIFI";\n'
        f'const char* UASH_API_URL = {json.dumps(args.api_url.rstrip("/"))};\n'
        f'const char* UASH_DEVICE_TOKEN = "{token}";\n')
    sql = f"""-- PostgreSQL only. Apply AFTER deploying controller migrations.
-- This registers/rotates only the controller credential; it never changes slot UUIDs.
BEGIN;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM slots WHERE public_id = '{slot}' AND deleted_at IS NULL) THEN
    RAISE EXCEPTION 'Slot public UUID does not exist';
  END IF;
END $$;
SELECT id FROM slots WHERE public_id = '{slot}' FOR UPDATE;
INSERT INTO devices (id, slot_id, token_hash, enabled, boot_id, command_id, revision,
 reservation_id, end_ms, ack_command_id, ack_on, created_at, updated_at)
SELECT '{device_id}', id, '{token_hash}', TRUE, '', '', 1, 0, 0, '', FALSE, NOW(), NOW()
FROM slots WHERE public_id = '{slot}' AND deleted_at IS NULL
ON CONFLICT (slot_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, enabled = TRUE,
 boot_id = '', command_id = '', revision = devices.revision + 1, reservation_id = 0,
 end_ms = 0, ack_command_id = '', ack_on = FALSE, ack_at = NULL,
 last_seen_at = NULL, updated_at = NOW();
COMMIT;
"""
    for name, text in [('secrets.h', config), ('register-controller.sql', sql)]:
        path = args.output_dir / name
        fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, 'w') as stream:
            stream.write(text)
    print('Private secrets.h and register-controller.sql created. No credentials printed.')

if __name__ == '__main__':
    main()
