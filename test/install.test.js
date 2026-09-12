import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  access,
  appendFile,
  chmod,
  mkdtemp,
  mkdir,
  readFile,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";

function releaseTarget() {
  const os = { darwin: "darwin", linux: "linux" }[process.platform];
  const arch = { arm64: "arm64", x64: "amd64" }[process.arch];
  if (!os || !arch) throw new Error(`unsupported test platform ${process.platform}/${process.arch}`);
  return `${os}_${arch}`;
}

test("installer rejects non-SemVer release selectors before downloading", () => {
  for (const version of ["vtest", "v1.2.3-01"]) {
    const result = spawnSync("sh", ["install.sh"], {
      cwd: process.cwd(),
      encoding: "utf8",
      env: {
        ...process.env,
        TRAJECTORY_VERSION: version,
      },
    });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, new RegExp(`invalid TRAJECTORY_VERSION: ${version.replaceAll(".", "\\.")}`));
  }
});

test("release tag validation uses the installer's SemVer rules", () => {
  for (const version of ["v0.1.0", "v1.2.3-rc.1+build.9"]) {
    const result = spawnSync("sh", ["install.sh", "--check-version", version], {
      cwd: process.cwd(),
      encoding: "utf8",
    });
    assert.equal(result.status, 0, `${version}: ${result.stderr}`);
  }
  for (const version of ["vtest", "v1.2.3-01", "v01.2.3", "v1.2"]) {
    const result = spawnSync("sh", ["install.sh", "--check-version", version], {
      cwd: process.cwd(),
      encoding: "utf8",
    });
    assert.notEqual(result.status, 0, version);
    assert.match(result.stderr, /invalid release version/);
  }
});

test("installer selects, verifies, and installs the release for this platform", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "trajectory-install-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const payload = join(root, "payload");
  const release = join(root, "release");
  const destination = join(root, "bin");
  await mkdir(payload);
  await mkdir(release);

  const binary = join(payload, "trajectory");
  await writeFile(binary, "#!/bin/sh\nprintf 'trajectory fixture\\n'\n");
  await chmod(binary, 0o755);

  const archiveName = `trajectory_${releaseTarget()}.tar.gz`;
  const archive = join(release, archiveName);
  const packed = spawnSync("tar", ["-czf", archive, "-C", payload, "trajectory"], {
    encoding: "utf8",
  });
  assert.equal(packed.status, 0, packed.stderr);
  const digest = createHash("sha256").update(await readFile(archive)).digest("hex");
  await writeFile(join(release, "checksums.txt"), `${digest}  ${archiveName}\n`);

  const installed = spawnSync("sh", ["install.sh"], {
    cwd: process.cwd(),
    encoding: "utf8",
    env: {
      ...process.env,
      TRAJECTORY_DOWNLOAD_ROOT: `file://${release}`,
      TRAJECTORY_INSTALL_DIR: destination,
    },
  });
  assert.equal(installed.status, 0, installed.stderr);
  assert.match(installed.stdout, /Installed trajectory fixture/);
  assert.equal((await stat(join(destination, "trajectory"))).mode & 0o111, 0o111);

  const invoked = spawnSync(join(destination, "trajectory"), [], { encoding: "utf8" });
  assert.equal(invoked.status, 0, invoked.stderr);
  assert.equal(invoked.stdout, "trajectory fixture\n");

  await appendFile(archive, "corrupt");
  const rejectedDestination = join(root, "rejected-bin");
  const rejected = spawnSync("sh", ["install.sh"], {
    cwd: process.cwd(),
    encoding: "utf8",
    env: {
      ...process.env,
      TRAJECTORY_DOWNLOAD_ROOT: `file://${release}`,
      TRAJECTORY_INSTALL_DIR: rejectedDestination,
    },
  });
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /checksum verification failed/);
  await assert.rejects(access(join(rejectedDestination, "trajectory")));

  await writeFile(binary, "#!/bin/sh\nexit 9\n");
  await chmod(binary, 0o755);
  const repacked = spawnSync("tar", ["-czf", archive, "-C", payload, "trajectory"], {
    encoding: "utf8",
  });
  assert.equal(repacked.status, 0, repacked.stderr);
  const badDigest = createHash("sha256").update(await readFile(archive)).digest("hex");
  await writeFile(join(release, "checksums.txt"), `${badDigest}  ${archiveName}\n`);

  const incompatible = spawnSync("sh", ["install.sh"], {
    cwd: process.cwd(),
    encoding: "utf8",
    env: {
      ...process.env,
      TRAJECTORY_DOWNLOAD_ROOT: `file://${release}`,
      TRAJECTORY_INSTALL_DIR: destination,
    },
  });
  assert.notEqual(incompatible.status, 0);
  const preserved = spawnSync(join(destination, "trajectory"), [], { encoding: "utf8" });
  assert.equal(preserved.status, 0, "failed upgrade replaced the working installation");
  assert.equal(preserved.stdout, "trajectory fixture\n");
});
