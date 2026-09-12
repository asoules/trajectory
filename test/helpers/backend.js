import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { query } from "./client.js";

// Exercise the production executable; no second parser or query implementation.
export async function backend(t, { twoTurns = false, changes = false } = {}) {
  const directory = await mkdtemp(join(tmpdir(), "trajectory-test-"));
  const args = [
    "serve",
    "-port",
    "0",
    "-db",
    join(directory, "investigations.sqlite"),
    "-home",
    directory,
  ];
  let session = "demo";
  if (twoTurns) {
    session = "11111111-1111-1111-1111-111111111111";
    const records = [],
      epoch = Date.parse("2026-09-01T00:00:00Z");
    const add = (type, payload, at) =>
      records.push(
        JSON.stringify({
          type,
          payload,
          timestamp: new Date(epoch + at).toISOString(),
        }),
      );
    add("session_meta", { id: session, cwd: changes ? "/repo" : undefined }, 0);
    for (let n = 1; n <= 2; n++) {
      const at = (n - 1) * 60000,
        turn_id = `turn-${n}`,
        parent = `parent-${n}`;
      add("event_msg", { type: "task_started", turn_id }, at);
      add(
        "response_item",
        {
          type: "message",
          role: "user",
          content: [
            {
              type: "input_text",
              text: n === 1 ? "First request\nMore detail" : "Second request",
            },
          ],
        },
        at,
      );
      add(
        "response_item",
        {
          type: "custom_tool_call",
          call_id: parent,
          name: "exec",
          input: "await test()",
        },
        at,
      );
      add(
        "event_msg",
        {
          type: "item_completed",
          turn_id,
          started_at_ms: epoch + at + 1000,
          completed_at_ms: epoch + at + 2000,
          item: {
            type: "CommandExecution",
            id: n === 1 ? "test-1" : "child-2",
            command: "npm test",
            exit_code: 0,
            stdout: "passed",
          },
        },
        at + 2000,
      );
      if (changes) {
        add(
          "event_msg",
          {
            type: "item_completed",
            turn_id,
            started_at_ms: epoch + at + 2500,
            completed_at_ms: epoch + at + 3000,
            item: {
              type: "FileChange",
              id: `edit-${n}`,
              changes:
                n === 1
                  ? {
                      "/repo/a.txt": { type: "add", content: "first" },
                      "/repo/b.txt": { type: "update", content: "second" },
                      "/repo/old.txt": {
                        type: "update",
                        move_path: "/repo/new.txt",
                        unified_diff: "not retained",
                      },
                    }
                  : {
                      "/repo/a.txt": { type: "update", content: "changed" },
                    },
            },
          },
          at + 3000,
        );
        add(
          "event_msg",
          {
            type: "item_completed",
            turn_id,
            started_at_ms: epoch + at + 3500,
            completed_at_ms: epoch + at + 4500,
            item: {
              type: "CommandExecution",
              id: `verify-${n}`,
              command: "go test ./...",
              exit_code: 0,
              stdout: "passed",
            },
          },
          at + 4500,
        );
      }
      add(
        "response_item",
        { type: "custom_tool_call_output", call_id: parent, output: "done" },
        at + 10000,
      );
      add("event_msg", { type: "task_complete", turn_id }, at + 10000);
    }
    await mkdir(join(directory, "sessions"));
    await writeFile(
      join(directory, "sessions", `rollout-${session}.jsonl`),
      records.join("\n") + "\n",
    );
  } else args.push("-demo");
  const child = spawn(
    process.env.TRAJECTORY_BIN ?? resolve("bin/trajectory"),
    args,
    { stdio: ["ignore", "ignore", "pipe"] },
  );
  const exited = once(child, "exit");
  t.after(async () => {
    child.kill("SIGTERM");
    await exited;
    await rm(directory, { recursive: true, force: true });
  });
  const baseURL = await new Promise((resolve, reject) => {
    let logs = "";
    const timer = setTimeout(
      () => reject(new Error(`Backend startup timeout: ${logs}`)),
      10000,
    );
    child.on("error", (error) => {
      clearTimeout(timer);
      reject(error);
    });
    child.on("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`Backend exited ${code}: ${logs}`));
    });
    child.stderr.on("data", (data) => {
      logs += data;
      const match = logs.match(/http:\/\/127\.0\.0\.1:\d+/);
      if (match) {
        clearTimeout(timer);
        resolve(match[0]);
      }
    });
  });
  const client = { baseURL };
  return {
    client,
    session,
    query: (method, args = {}) => query(client, method, args),
  };
}
