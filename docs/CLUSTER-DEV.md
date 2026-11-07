# Isolated local development cluster

From the repository root, run:

```sh
npm run dev:cluster -- up        # build, start and bootstrap
npm run dev:cluster -- up --web  # also start the Vite dashboard
npm run dev:cluster -- smoke     # exercise routing, ledger, re-join and Claude fidelity
npm run dev:cluster -- smoke --skip-fidelity # omit Claude fidelity checks
npm run dev:cluster -- status    # run cluster status on each node
npm run dev:cluster -- logs      # follow all logs; Ctrl-C stops tailing
npm run dev:cluster -- logs member-a # follow one log
npm run dev:cluster -- down      # stop the recorded processes, retain data
npm run dev:cluster -- reset     # stop processes and delete all cluster data
```

Go 1.26 or newer and Node are required. For `--web`, install the existing web dependencies first (`npm install --prefix web`). The dashboard is at http://127.0.0.1:28175/dashboard/; `up --web` prints the hub management secret for login. Each node's client key and management secret are also in its `env.json`; these files contain secrets and should remain private.

All listeners bind to loopback: hub 28171, member-a 28172, member-b 28173, fake upstream 28179, and optional Vite 28175. This tool never uses ports 28120 or 28122. It does not use the repository's `tproxy.db` or `~/.tproxy`.

Everything is under gitignored `.dev/cluster/`: `bin/tproxy` is the built binary; each node directory holds `config.yaml`, `tproxy.db`, `env.json`, `log`, and `pid.json`. The upstream and dashboard have their own log and PID files. Each process gets an isolated HOME. `down` verifies recorded process identities before stopping them; it does not stop unrelated processes. `up` preserves existing cluster data and keys; `reset` deletes them. The fake upstream returns the account bearer in its response, allowing the smoke test to identify which node served each request.

The hub shares `dev-claude` through an Anthropic-compatible provider. The fake
upstream's `/v1/messages` selects a response from a marker in the last user
message: `scenario:tools2` returns two interleaved tool calls,
`scenario:maxtokens` returns `max_tokens`, `scenario:stopseq` returns stop
sequence `END`, and `scenario:thinking` returns signed thinking and redacted
thinking blocks. Both streaming and non-streaming responses include the
upstream bearer in text so the checks can confirm the hub served them.

By default, `smoke` checks each scenario in both modes directly on the hub and
through member-b, reporting a PASS or FAIL and a short expected/actual diff. It
exits nonzero if any check fails. A rate-limited request is retried once after
65 seconds to let the cluster lending window expire. Use `--skip-fidelity` to run the original
routing, ledger, and re-join checks alone. Existing cluster data is retained;
`up` adds the provider, model, and shared catalog entry when missing.
