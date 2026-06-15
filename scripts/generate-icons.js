const sharp = require('sharp');
const path = require('path');
const fs = require('fs');

const svgPath = path.join(__dirname, '..', 'radar-extension', 'icon.svg');
const outDir = path.join(__dirname, '..', 'radar-extension', 'icons');

fs.mkdirSync(outDir, { recursive: true });

const sizes = [16, 48, 128];

(async () => {
  for (const size of sizes) {
    const out = path.join(outDir, `icon-${size}.png`);
    await sharp(svgPath).resize(size, size).png().toFile(out);
    console.log(`Generated ${out}`);
  }
})();
