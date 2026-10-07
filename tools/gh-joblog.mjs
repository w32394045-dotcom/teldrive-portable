// Download a GitHub Actions job log (a zip) to a local file.
//   node gh-joblog.mjs <owner/repo> <runId> <outFile>
import fs from "node:fs";
import https from "node:https";

const [repo, runId, outFile] = process.argv.slice(2);
const token = JSON.parse(fs.readFileSync(process.env.USERPROFILE + "/.dsh/github.json", "utf8")).token;

function get(path, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > 5) return reject(new Error("too many redirects"));
    https
      .get(
        {
          hostname: "api.github.com",
          path,
          headers: {
            authorization: `Bearer ${token}`,
            accept: "application/vnd.github+json",
            "user-agent": "dsh-gh-logs",
            "x-github-api-version": "2022-11-28",
          },
        },
        (r) => {
          if ([301, 302, 303, 307, 308].includes(r.statusCode) && r.headers.location) {
            r.resume();
            resolve(get(r.headers.location, redirects + 1));
            return;
          }
          resolve(r);
        },
      )
      .on("error", reject);
  });
}

function apiJson(path) {
  return get(path).then(
    (r) =>
      new Promise((resolve, reject) => {
        let body = "";
        r.on("data", (c) => (body += c));
        r.on("end", () => {
          if (r.statusCode >= 300) return reject(new Error(`HTTP ${r.statusCode}: ${body.slice(0, 200)}`));
          resolve(JSON.parse(body));
        });
      }),
  );
}

const jobs = await apiJson(`/repos/${repo}/actions/runs/${runId}/jobs`);
const job = (jobs.jobs ?? []).find((j) => j.conclusion === "failure") ?? (jobs.jobs ?? [])[0];
console.log(`job ${job.id} ${job.name} [${job.conclusion}]`);

// Prefer the raw logs endpoint (follows a redirect to a signed URL).
const res = await get(`/repos/${repo}/actions/jobs/${job.id}/logs`);
const chunks = [];
for await (const chunk of res) chunks.push(chunk);
fs.writeFileSync(outFile, Buffer.concat(chunks));
console.log(`saved ${outFile} (${fs.statSync(outFile).size} bytes)`);
