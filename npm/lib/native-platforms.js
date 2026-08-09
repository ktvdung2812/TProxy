const nativeTargets = Object.freeze([
  { platform: "darwin", arch: "arm64", goos: "darwin", goarch: "arm64" },
  { platform: "darwin", arch: "x64", goos: "darwin", goarch: "amd64" },
  { platform: "linux", arch: "x64", goos: "linux", goarch: "amd64" },
  { platform: "linux", arch: "arm64", goos: "linux", goarch: "arm64" },
  { platform: "win32", arch: "x64", goos: "windows", goarch: "amd64" },
  { platform: "win32", arch: "arm64", goos: "windows", goarch: "arm64" },
]);

function findNativeTarget(platform = process.platform, arch = process.arch) {
  return nativeTargets.find((target) => target.platform === platform && target.arch === arch);
}

function nativeBinaryFileName(target) {
  const extension = target.platform === "win32" ? ".exe" : "";
  return `tproxy-${target.platform}-${target.arch}${extension}`;
}

module.exports = { findNativeTarget, nativeBinaryFileName, nativeTargets };
