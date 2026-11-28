# pushq

## Documentation rules

These apply to docs in this repo (`README.md`, `docs/`) and to the docs site
(`../pushq-docs`, https://pushqueue.dev/docs):

- **Never document SaaS mode.** Don't write about running pushq in SaaS mode
  (WorkOS settings, `WORKOS_*` variables, operator keys, multi-team
  self-hosting). Features people use on the hosted service (teams, email-code
  and GitHub sign-in) are described as "the hosted service", never as "SaaS
  mode".
- **Docs about anything in the console need screenshots.** Take them from the
  real console in Chrome on the Mac (headless via `puppeteer-core`, against a
  local server with realistic demo data), zoom each to the area the text is
  about, and frame them with the Claude Design canvas "pushq docs
  screenshots" (macOS window on the pushqueue.dev backdrop). The full
  pipeline is in `../pushq-docs/CLAUDE.md`.
