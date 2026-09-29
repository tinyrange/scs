# Disposable sandboxed build demonstration

The fixture under `examples/session/` validates a real C compile and execution
against API-edited native files. It uses a tiny fresh repository, not the retained
Linux artifact. It needs the host's separately approved `session_demo` helper.
That helper is development configuration, not part of the SCS sandbox boundary.

The approved helper exposes only a disposable factory and fixed operations:

```python
demo = session_demo.new("success")  # also "failure" or "timeout"
demo.mount()
result = demo.build()  # fixed fixture, actual host sandbox=True
end = demo.unmount()   # ordinary unmount, never force/lazy
proof = demo.verify()
```

The host starts with `scs init`, seeds `main`, and creates an unchanged sibling.
The session's before script edits the greeting in `hello.c` and the expected
output. The fixed Python driver compiles with `cc`, executes the new ELF program,
and compares its output with the API-edited expectation. It records per-phase
status/timing, bounded child output files, and artifact SHA-256/size/mode/mtime.

The host runs that driver with `sandbox=True`, a fixed mount cwd, a cleared
and fixed environment, and `/usr/include` as a read-only grant. It writes a
separate host receipt **outside** the mount, using the outer process status and
this session's token. Unmount durably publishes the candidate; post-inspection
and independent CLI reopen check artifact hashes/sizes/modes and source/sibling
identity. The current reopen verifier does not compare artifact mtime values.

Modes:

- `success`: compile and execute; runner and cah both exit zero.
- `failure`: `-DFORCE_FAILURE` triggers a real compiler diagnostic; logs remain
  inspectable and durable, no executable is expected, and cah exits nonzero.
- `timeout`: compile and execute successfully, then time out a controlled sleeping
  subprocess; the fixed driver exits 124, the host marks timeout, and cah fails
  while retaining the compiled artifact and logs.

The Python driver is **not itself a sandbox**. It uses a fresh process group for
each phase, kills remaining group members, and applies core/file-size/CPU limits.
It does not claim containment of hostile descendants that escape that group;
external sandbox/process supervision remains required. No package installation,
caller-selected arbitrary command, or unsandboxed fallback is exposed by the
approved helper. Missing compiler/header dependencies are failures to report.

Always retain the demo object until ordinary unmount completes. On build errors,
inspect `demo.diagnostics()` and still attempt ordinary unmount. Do not force
cleanup or assume failed unmount means the mount disappeared. Temporary evidence
remains associated with the demo object; copy sanitized proof JSON into the project
if it should outlive the development session.

## Status

Validated on September 29, 2026 using the approved helper, with real host
sandboxing enabled throughout:

| Case | Fixture exit | cah exit | Reopened artifacts verified |
| --- | ---: | ---: | ---: |
| Successful compile + execution | 0 | 0 | 7 |
| Intentional compiler failure | 1 | 1 | 4 |
| Compile + execution + controlled timeout | 124 | 1 | 9 |

Every case completed read-only inspection, ordinary unmount, independent native
integrity/reopen checks, and verification that main/sibling roots were unchanged.
The failure driver checked that the diagnostic was the intended `#error`, rather
than accepting any compiler failure. The timeout was in the controlled child
phase, not exhaustion of the outer host's 120-second deadline. All mount daemons
exited and no test mount was left running. No packages were installed.

Raw sanitized evidence is in `session-build-results.json`. Timings are observed
fixture timings, not performance comparisons. The first verification attempt
hit a helper JSON keyword-argument bug after its assertions; the helper-only fix
was separately approved, and the successful proof was rerun and recorded.
