const fs = require("fs");
const path = require("path");
const https = require("https");
const { execSync } = require("child_process");

const REPO = "hoijunkim/shape";
const version = require("./package.json").version;

const platform = { linux: "linux", darwin: "darwin", win32: "windows" }[process.platform];
const arch = { x64: "amd64", arm64: "arm64" }[process.arch];
if (!platform || !arch) {
  console.error(`shape: unsupported platform ${process.platform}/${process.arch}`);
  process.exit(1);
}

const ext = platform === "windows" ? "zip" : "tar.gz";
const asset = `shape_${version}_${platform}_${arch}.${ext}`;
const url = `https://github.com/${REPO}/releases/download/v${version}/${asset}`;
const binDir = path.join(__dirname, "bin");
fs.mkdirSync(binDir, { recursive: true });

function download(u, dest, redirects = 0) {
  return new Promise((resolve, reject) => {
    https.get(u, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        if (redirects > 10) return reject(new Error("too many redirects"));
        return resolve(download(res.headers.location, dest, redirects + 1));
      }
      if (res.statusCode !== 200) return reject(new Error(`HTTP ${res.statusCode} for ${u}`));
      const out = fs.createWriteStream(dest);
      res.pipe(out);
      out.on("finish", () => out.close(resolve));
    }).on("error", reject);
  });
}

(async () => {
  const archivePath = path.join(binDir, asset);
  await download(url, archivePath);
  if (ext === "zip") {
    execSync(`tar -xf "${archivePath}" -C "${binDir}"`); // bsdtar on win handles zip
  } else {
    execSync(`tar -xzf "${archivePath}" -C "${binDir}"`);
  }
  fs.rmSync(archivePath);
  const bin = path.join(binDir, platform === "windows" ? "shape.exe" : "shape");
  if (platform !== "windows") fs.chmodSync(bin, 0o755);
})().catch((e) => {
  console.error("shape: failed to download the binary:", e.message);
  process.exit(1);
});
