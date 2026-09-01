// secrets are not in wrangler.jsonc, so `wrangler types` cannot see them and
// they are missing from the generated worker-configuration.d.ts. declared here
// instead, which also means regenerating those types does not wipe this.
interface Env {
  // fine-grained pat on M1noa/proxypool: actions read+write, contents read.
  // set with `wrangler secret put GH_PAT`. see src/watchdog.ts.
  GH_PAT: string;
}
