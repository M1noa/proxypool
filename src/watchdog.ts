// cron watchdog: github's scheduler is the primary trigger for the fetch
// workflow, and it is unreliable -- on a single hourly cron that repo measured
// 36 fires in 151h, 23% delivery. the workflow now offers twelve slots an hour
// and gates them itself, so all this has to cover is github dropping every one.

const REPO = "M1noa/proxypool";

// the workflow's own cadence gate runs at 35 min. sitting well past it means a
// healthy repo never reaches this path, and a dispatch only happens once about
// four consecutive slots have gone missing.
const STALE_MINUTES = 55;

function ghHeaders(env: Env): Record<string, string> {
  return {
    accept: "application/vnd.github+json",
    authorization: `Bearer ${env.GH_PAT}`,
    "user-agent": "proxies-minoa-cat-watchdog",
  };
}

// minutes since the output branch was last force-pushed, which the workflow's
// publish step does every run. null when github did not answer usefully: an api
// failure is not evidence the pipeline stalled, so the caller stays put.
async function minutesSincePublish(env: Env): Promise<number | null> {
  const res = await fetch(`https://api.github.com/repos/${REPO}/commits/output`, {
    headers: ghHeaders(env),
  });
  if (!res.ok) {
    console.log(JSON.stringify({ message: "watchdog: commits read failed", status: res.status }));
    return null;
  }
  const body = (await res.json()) as { commit?: { committer?: { date?: string } } };
  const date = body.commit?.committer?.date;
  if (!date) return null;
  const ms = Date.parse(date);
  return Number.isNaN(ms) ? null : (Date.now() - ms) / 60_000;
}

export async function watchdog(env: Env): Promise<void> {
  // no token configured is the normal state of a fresh preview deploy, not an
  // error worth logging every cron tick.
  if (!env.GH_PAT) return;

  const age = await minutesSincePublish(env);
  if (age === null || age < STALE_MINUTES) return;

  // gated, deliberately. the workflow's guard answers "due" and exits before
  // its in-flight check when a dispatch is ungated, so ungated here could open
  // a second run alongside one already going.
  const res = await fetch(
    `https://api.github.com/repos/${REPO}/actions/workflows/fetch.yml/dispatches`,
    {
      method: "POST",
      headers: { ...ghHeaders(env), "content-type": "application/json" },
      body: JSON.stringify({ ref: "main", inputs: { gated: "true" } }),
    },
  );

  console.log(
    JSON.stringify({
      message: "watchdog dispatched fetch.yml",
      ageMinutes: Math.round(age),
      status: res.status, // 204 on success
    }),
  );
}
