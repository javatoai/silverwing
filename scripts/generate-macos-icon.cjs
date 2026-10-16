#!/usr/bin/env node
// Regenerate from the repository root: node scripts/generate-macos-icon.cjs
// Authoring dependencies: Node.js and sharp 0.35.5 available to require()
// (normal Node module resolution or NODE_PATH). Release builds consume the
// checked-in ICNS; they need neither tool and keep the Windows icon intact.
const fs = require("node:fs");
const path = require("node:path");
const sharp = require("sharp");

async function main() {
  const assets = path.join(__dirname, "..", "internal", "desktop", "assets");
  const source = fs.readFileSync(path.join(assets, "app-icon-macos.svg"));
  const images = new Map();
  for (const size of [16, 32, 64, 128, 256, 512, 1024]) {
    images.set(size, await sharp(source).resize(size, size).png().toBuffer());
  }
  const chunks = [];
  for (const [code, size] of [
    ["icp4", 16], ["ic11", 32], ["icp5", 32], ["ic12", 64],
    ["ic07", 128], ["ic13", 256], ["ic08", 256],
    ["ic14", 512], ["ic09", 512], ["ic10", 1024],
  ]) {
    const image = images.get(size);
    const header = Buffer.alloc(8);
    header.write(code, 0, 4, "ascii");
    header.writeUInt32BE(image.length + 8, 4);
    chunks.push(header, image);
  }
  const body = Buffer.concat(chunks);
  const header = Buffer.alloc(8);
  header.write("icns", 0, 4, "ascii");
  header.writeUInt32BE(body.length + 8, 4);
  fs.writeFileSync(path.join(assets, "app-icon.icns"), Buffer.concat([header, body]));
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
