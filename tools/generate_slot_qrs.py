"""Render QR assets from an existing UUID manifest; never generate or rotate IDs.

Install: python -m pip install 'qrcode[pil]==8.2'
Run: python tools/generate_slot_qrs.py docs/staging-slot-qrs/slots.json
"""
import argparse
import html
import json
from pathlib import Path
import uuid

import qrcode
import qrcode.image.svg


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('manifest', type=Path)
    args = parser.parse_args()
    data = json.loads(args.manifest.read_text())
    out = args.manifest.parent
    articles = []
    seen = set()
    for slot in data['slots']:
        public_id = slot['public_id']
        parsed = uuid.UUID(public_id)
        if parsed.version != 4 or str(parsed) != public_id or public_id in seen:
            raise ValueError('Expected distinct canonical UUID v4 identifiers')
        seen.add(public_id)
        payload = f'uash://slot/{public_id}'
        filename = f'espacio-{slot["id"]}'
        qr = qrcode.QRCode(error_correction=qrcode.constants.ERROR_CORRECT_M, box_size=16, border=4)
        qr.add_data(payload)
        qr.make(fit=True)
        qr.make_image(fill_color='black', back_color='white').save(out / f'{filename}.png')
        qr.make_image(image_factory=qrcode.image.svg.SvgPathImage).save(out / f'{filename}.svg')
        articles.append(f'''<article><p class="env">UASH · {html.escape(data['environment'].upper())}</p>
<h1>{html.escape(slot['label'])}</h1><img src="{filename}.png" alt="QR de {html.escape(slot['label'])}">
<p>Escaneá el QR desde Uash</p><code>{html.escape(public_id)}</code></article>''')
    page = '''<!doctype html><html lang="es"><meta charset="utf-8"><title>Uash · QR de espacios de staging</title>
<style>@page{size:A4;margin:12mm}*{box-sizing:border-box}body{margin:0;font-family:Arial,sans-serif;color:#152742;background:white}
main{display:grid;grid-template-columns:1fr 1fr;gap:8mm}article{text-align:center;border:1px dashed #9aa7b9;border-radius:4mm;padding:6mm 3mm;break-inside:avoid}
h1{font-size:22pt;margin:2mm 0}.env{font-size:10pt;letter-spacing:1px;margin:0}img{display:block;width:66mm;height:66mm;margin:2mm auto}
p{font-size:11pt}code{font-size:7pt;color:#44556b}@media screen{body{padding:24px;max-width:850px;margin:auto}}
</style><main>''' + ''.join(articles) + '</main></html>\n'
    (out / 'imprimir.html').write_text(page)
    print(f'Generated {len(seen)} PNGs, {len(seen)} SVGs and imprimir.html in {out}')


if __name__ == '__main__':
    main()
