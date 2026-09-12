import { spawn } from "node:child_process";
import { once } from "node:events";
import { resolve } from "node:path";

export async function cli(args, { input = "", cwd, env } = {}) {
  const child = spawn(
    process.env.TRAJECTORY_BIN ?? resolve("bin/trajectory"),
    args,
    {
      cwd,
      env,
      stdio: ["pipe", "pipe", "pipe"],
      timeout: 30000,
    },
  );
  let stdout = "",
    stderr = "";
  child.stdout.setEncoding("utf8").on("data", (chunk) => {
    stdout += chunk;
  });
  child.stderr.setEncoding("utf8").on("data", (chunk) => {
    stderr += chunk;
  });
  const closed = once(child, "close");
  child.stdin.end(input);
  const [code, signal] = await closed;
  return { code, signal, stdout, stderr };
}
