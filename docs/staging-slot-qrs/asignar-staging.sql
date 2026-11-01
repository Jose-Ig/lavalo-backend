-- STAGING ONLY. Run before the first deployment of the UUID-based QR backend.
-- Re-running with the same IDs is safe. A conflicting UUID aborts the transaction.
BEGIN;
LOCK TABLE slots IN ACCESS EXCLUSIVE MODE;
ALTER TABLE slots ADD COLUMN IF NOT EXISTS public_id varchar(36);

DO $$
BEGIN
    IF (SELECT count(*) FROM slots WHERE id IN (1,2,3,4) AND deleted_at IS NULL) <> 4 THEN
        RAISE EXCEPTION 'Expected active staging spaces 1,2,3,4; no QR mappings were applied';
    END IF;
    IF EXISTS (
        SELECT 1 FROM slots s
        JOIN (VALUES
        (1, '1493790d-6574-4a92-8445-afd174027bc0'),
        (2, '2402c8d5-c224-4ef1-8f4d-d2c19f85ab7a'),
        (3, '7491ec4c-9a89-4ebe-95d6-6f4c87c3329a'),
        (4, 'bc1b69aa-d22f-4aae-a560-fea75eac64a9')
        ) AS expected(id, public_id) ON expected.id = s.id
        WHERE s.public_id IS NOT NULL AND s.public_id <> '' AND s.public_id <> expected.public_id
    ) THEN
        RAISE EXCEPTION 'A space already has a different UUID. Export its actual UUID and regenerate the QR; do not rotate existing IDs';
    END IF;
END $$;

UPDATE slots AS s SET public_id = expected.public_id
FROM (VALUES
        (1, '1493790d-6574-4a92-8445-afd174027bc0'),
        (2, '2402c8d5-c224-4ef1-8f4d-d2c19f85ab7a'),
        (3, '7491ec4c-9a89-4ebe-95d6-6f4c87c3329a'),
        (4, 'bc1b69aa-d22f-4aae-a560-fea75eac64a9')
) AS expected(id, public_id)
WHERE s.id = expected.id AND (s.public_id IS NULL OR s.public_id = '');

CREATE UNIQUE INDEX IF NOT EXISTS idx_slots_public_id ON slots(public_id);
COMMIT;

SELECT id, label, public_id FROM slots WHERE id IN (1,2,3,4) ORDER BY id;
