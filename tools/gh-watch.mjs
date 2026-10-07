// Watch a GitHub Actions run until it finishes, then print job steps.
//   node gh-watch.mjs <owner/repo> <runId> [timeoutSeconds]
import fs from "node:fs";
import https from "node:https";

const [repo, runId, timeoutArg] = process.argv.slice(2);
const timeout = Number(timeoutArg ?? 900) * 1000;
const token = JSON.parse(fs.readFileSync(process.env.USERPROFILE + "/.dsh/github.json", "utf8")).token;

function api(path) {
  return new Promise((resolve, reject) => {
    https
      .get(
        {
          hostname: "api.github.com",
          path,
          headers: {
            authorization: `Bearer ${token}`,
            accept: "application/vnd.github+json",
            "user-agent": "dsh-gh-watch",
            "x-github-api-version": "2022-11-28",
          },
        },
        (r) => {
          let body = "";
          r.on("data", (c) => (body += c));
          r.on("end", () => {
            if (r.statusCode >= 300) return reject(new Error(`HTTP ${r.statusCode}: ${body.slice(0, 200)}`));
            resolve(JSON.parse(body));
          });
        },
      )
      .on("error", reject);
  });
}

const started = Date.now();
let run = await api(`/repos/${repo}/actions/runs/${runId}`);
process.stdout.write(`run #${run.run_number} ${run.head_branch} status=${run.status}\n`);
while (run.status !== "completed" && Date.now() - started < timeout) {
  await new Promise((r) => setTimeout(r, 20000));
  run = await api(`/repos/${repo}/actions/runs/${runId}`);
  process.stdout.write(`  … status=${run.status} conclusion=${run.conclusion ?? "-"} (${Math.round((Date.now() - started) / 1000)}s)\n`);
}

const jobs = await api(`/repos/${repo}/actions/runs/${runId}/jobs`);
for (const job of jobs.jobs ?? []) {
  console.log(`\nJOB ${job.name} [${job.status}/${job.conclusion ?? "-"}]`);
  for (const step of job.steps ?? []) {
    console.log(`  ${step.conclusion === "success" ? "OK  " : step.conclusion === "skipped" ? "skip" : "!!  "} ${step.name} -> ${step.conclusion ?? step.status}`);
  }
}
console.log(`\nconclusion=${run.conclusion} url=${run.html_url}`);
process.exit(run.conclusion === "success" ? 0 : 1);
