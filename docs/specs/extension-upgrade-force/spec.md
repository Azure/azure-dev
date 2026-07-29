# Unblocking Extension Upgrade and Uninstall When the Extension Is Running

Issue: https://github.com/Azure/azure-dev/issues/9307

## Problem

On Windows, `azd extension upgrade` fails whenever the extension being upgraded
has a live process. The user sees:

```
failed to uninstall extension: failed to remove extension: ... Access is denied.
```

The cause is structural, not transient. `upgradeInternal` uninstalls before it
installs (`pkg/extensions/manager.go`), and `Uninstall` calls
`osutil.RemoveAll` on the extension directory. Windows refuses to delete a file
that is mapped as a running image, so the delete returns `ERROR_ACCESS_DENIED`.
`osutil.RemoveAll` retries ten times at a one second interval
(`pkg/osutil/rename_windows.go`), which only helps when the lock is momentary.
A genuinely running extension never releases inside that window, so the retry
loop is guaranteed to exhaust.

This is not rare. Long-lived extensions are the norm: anything running an MCP
server, a dev server, a watcher, or a background daemon holds its own binary
open. The extension that triggered this report had three live processes at the
time of the failed upgrade. The user gets a low-level Win32 error with no
indication of which process is at fault or what to do about it, and the only
recovery is to hunt down and stop the processes by hand.

On macOS and Linux the delete succeeds because those platforms unlink by
directory entry rather than by image. The upgrade appears to work, but the old
process keeps executing the now-unlinked binary, so the user believes they are
running the new version when they are not.

## Goals

- `azd extension upgrade` and `azd extension uninstall` succeed by default even
  when the extension has running processes, without stopping anything.
- `--force` on both commands stops the extension's own processes so the new
  version takes effect immediately.
- Process discovery is provably scoped to executables inside the extension's
  install directory. Unrelated processes are never candidates.
- Failures that remain report which processes are blocking, by name and PID,
  and point at `--force`.
- Behavior is deterministic under `--no-prompt` for CI and scripted use.
- Files that had to be set aside are cleaned up automatically on later
  extension operations rather than accumulating.

## Non-Goals

- Terminating the extension's child process tree. Only the extension binary
  itself is a candidate. See Solution for the reasoning.
- A general purpose process management API for azd or for extension authors.
- Asking extensions to shut down over gRPC. Extensions are not required to be
  running or reachable, so a transport-level shutdown handshake cannot be the
  mechanism that unblocks a file operation.
- Changing the uninstall-then-install ordering in `upgradeInternal`. That
  ordering is fine once uninstall stops failing.
- Reconciling the process query code that already exists in `pkg/azdext`. See
  Risks.

## Acceptance Criteria

These are the identifiers the `Source` column in
[`test-plan.md`](./test-plan.md) cites. Each one is a behavior that has to hold,
stated so a reviewer can check a test row against it.

| Id | Criterion |
| --- | --- |
| AC-1 | Files that cannot be deleted because a process holds them open are relocated out of the extension directory instead of failing the operation, and relocation is a no-op when nothing is locked. |
| AC-2 | `azd extension upgrade` and `azd extension uninstall` succeed by default, with no `--force`, while the extension has running processes. Removing a path that does not exist is also a success. |
| AC-3 | `azd extension upgrade <id> --force` succeeds while the extension has running processes, stopping them gracefully first and escalating to a forceful stop only after the grace period expires. |
| AC-4 | `azd extension uninstall <id> --force` behaves the same way, and stopping processes in a directory with none running is a no-op rather than an error. |
| AC-5 | Process discovery is provably scoped to executables inside the extension's install directory. Empty and root scopes are refused, prefix siblings do not match, and unrelated processes are never candidates. |
| AC-6 | The command reports which processes it stopped, by name and PID, on both human-readable and `--output json` paths. |
| AC-7 | Without `--force`, the failure names the blocking processes and points at `--force`, rather than surfacing a bare permission error. A blocked removal with no discoverable process still explains itself. |
| AC-8 | Behavior is deterministic under `--no-prompt` for CI and scripted use. Termination is bounded by the grace period and honors context cancellation. |
| AC-9 | Discovery works on Windows, macOS, and Linux, including the case where a stale process keeps running an unlinked binary on non-Windows platforms, and follows each platform's path casing rules. |
| AC-10 | `--force` composes with `--all`, stopping processes for every extension in the run rather than only the first. |
| AC-11 | Relocated files are swept on later extension operations. Entries still held open are left in place, and a sweep failure never fails the command. |
| AC-12 | `--force` is registered on both commands and bound to their options, with help text, usage snapshots, and completion metadata updated. |

## Solution

Two independent layers. The first makes the failure stop happening. The second
makes the upgrade take effect immediately.

### Layer 1: relocate instead of delete (default, non-destructive)

Windows blocks deleting a running image but permits renaming it. The image
loader opens executables with `FILE_SHARE_DELETE`, so rename, which is a
delete-class operation on the directory entry, is allowed, while true deletion
is blocked by the section mapping. This is the same technique Chrome and the Go
toolchain use to replace themselves while running.

Verified empirically on Windows 11:

| Operation on a running exe | Result |
|---|---|
| Delete | fails, access denied |
| Rename within the same directory | succeeds |
| Rename across directories on the same volume | succeeds |
| Write a new exe at the original path after rename | succeeds |
| Remove the now-empty parent directory | succeeds |
| Delete the relocated exe while it is still running | fails, access denied |
| Delete the relocated exe after the process exits | succeeds |
| Process stays alive throughout | yes |

`Uninstall` gains a relocation fallback. When `RemoveAll` cannot remove a path,
the locked file is renamed into a trash directory that sits outside the
extension directory, at `<configDir>/extensions/.trash/`. Relocating outside
rather than within matters: the goal is for the extension directory itself to
become removable, which it does once the locked entry is no longer inside it.
Subsequent install, upgrade, and uninstall operations sweep the trash directory
on a best-effort basis, so entries disappear once the holding process exits.

The user-visible result is that upgrade stops failing. The old process keeps
running the old code until it is restarted, which matches the existing
macOS and Linux behavior and is the honest outcome for a non-destructive
operation.

Trash destinations are named `<original>.<pid>.<random>` rather than picked by
scanning for a free name. Choosing a destination and renaming onto it cannot be
made one operation, so a name derived from what is currently on disk is
inherently racy: two azd processes relocating the same base name both observe the
same free path and both rename onto it. On Windows `os.Rename` replaces the
destination, so the collision is not even reported: one of them either clobbers a
file the other still needs or fails against a destination that is itself a locked
executable, and the uninstall it belonged to fails after its retries. Detecting
the conflict after the fact is therefore not available, and the name itself has
to prevent it. The sweep deletes every child of the trash directory, so the
naming scheme costs nothing at cleanup time.

### Layer 2: `--force` (opt-in, destructive)

`--force` on `azd extension upgrade` and `azd extension uninstall` discovers
processes whose executable lives inside the extension's install directory and
stops them: a graceful signal first, a short grace period, then a forceful
terminate for anything still alive. Stopped processes are reported by name and
PID.

`--force` is deliberately the same word already used by `azd extension install
--force`. Install uses it to override the already-installed and downgrade
guards. Upgrade and uninstall use it to override a running-process guard. The
unifying contract is "override what is blocking me", with the blocker differing
by command. `--force` does not prompt, because opting in is the confirmation.

Discovery lives in a new `pkg/processutil` package with a small surface:
`ProcessInfo`, `FindByExecutableDir(ctx, dir)`, and
`Terminate(ctx, process, scope, grace)`. It mirrors the `_windows.go` /
`_darwin.go` build-tag split already used by `pkg/azdext/process*.go`, with the
procfs implementation named `processutil_procfs.go` rather than `_linux.go` so
that its `!windows && !darwin` tag is load-bearing and genuinely covers the
other procfs platforms. Windows enumerates with `CreateToolhelp32Snapshot` plus
`QueryFullProcessImageName`; procfs platforms read `/proc/<pid>/exe`; macOS
shells out to `/bin/ps`.

Because this terminates processes, containment is the load-bearing safety
property, and it is enforced in two independent places for two distinct reasons.

`FindByExecutableDir` rejects an empty directory, rejects root paths such as `/`
and `C:\` where containment would match everything, resolves to an absolute path
and evaluates symlinks before comparing, excludes the current azd process, and
compares using the existing `osutil.IsPathContained`, which is already the
repository's idiom for this class of check and handles backslash normalization
and Windows case-insensitivity.

`Terminate` then takes the whole `ProcessInfo` plus the same scope, rather than a
bare PID. It first re-checks the declared executable against the scope, which is
a caller-contract check that makes it impossible to reach the signal path with a
PID sourced from anywhere other than a scoped discovery. It then re-reads live
operating system state immediately before each signal and confirms the PID still
resolves to an executable inside the scope. That second check narrows the
time-of-check to time-of-use window: between discovery and termination the
original process can exit and the operating system can reissue its PID to an
unrelated program. When the live check finds the process gone, `Terminate`
returns `(false, nil)` so the caller reports nothing rather than claiming a stop
that never happened.

Narrowing is not closing, because a check by PID followed by a signal by PID is
still two lookups by number. The forceful stop therefore validates scope a third
time against a pinned process identity. On Windows `forceKill` opens the process
once with `PROCESS_TERMINATE | PROCESS_QUERY_LIMITED_INFORMATION`, reads the
image path through that handle, and calls `TerminateProcess` on the same handle.
Windows keeps a process id reserved for as long as any handle to that process
object is open, so the identity the scope check accepted cannot be swapped for
another process before the kill lands. A process that fails the check there is
refused with `ErrProcessOutOfScope` rather than terminated.

Unix has no portable equivalent. Linux could use a pidfd and macOS offers
nothing of the sort, so the SIGTERM and SIGKILL paths keep a residual window
bounded by the re-check immediately preceding them. That is an accepted risk
rather than an oversight: the platform this feature exists for is the one where
the window is closed, and a Unix `--force` that stopped the wrong process would
require the target to exit and its PID to be reissued inside a window measured in
microseconds.

Stopped processes are reported by name and PID on the console, and carried in
`UpgradeResult.StoppedProcesses`, which serializes as `stoppedProcesses` under
`--output json`. Console reporting is suppressed under `--output json`, so
without the structured field a scripted caller would have no way to learn that
`--force` terminated anything.

### Extension id is a path component, not a path

The extension id reaches the filesystem as a directory name, and before this
change several call sites built paths from it with a bare `filepath.Join`. A
single `extensionPaths(userConfigDir, id)` choke point now derives every
extension path. Install, uninstall, and all four metadata operations route
through it.

Validation happens in two stages, because neither catches what the other does.

`validateExtensionId` runs first, on the raw id, before any join. It requires
that the id be usable as a single path component: no separators, no `:`, no
control characters, not empty, `.` or `..`, not the reserved `.trash` name, and
not a legacy DOS device name such as `CON` or `COM1`. It also rejects any id
ending in a dot or a space.

That last rule exists because Win32 silently strips trailing dots and spaces
from a path component and Go's `filepath` package does not model it. Verified on
Windows 11: `os.MkdirAll` of `.trash.`, `.trash `, `foo.` and `foo..` creates
`.trash` and `foo`, and a subsequent `os.ReadDir` shows only the stripped names.
So `filepath.Base(".trash.")` returns `".trash."`, which does not match a
comparison against `.trash` while the operating system happily operates on the
real relocation directory. The same aliasing lets the id `foo.` resolve onto the
directory owned by the extension `foo`, so uninstalling one would delete the
other's files. Matching against what a path component may contain, rather than
against a list of known-bad shapes, means a variation nobody anticipated fails
closed.

`extensionPaths` then requires that `filepath.Dir(extensionDir)` equal the
extensions root. That comparison forces the id to be exactly one directory
level and is kept as a second line of defense. `IsPathContained` on its own
would not do, because it returns true for equal paths and for legitimately
nested ones.

### Directories azd owns must not be links

`--force` derives a process-termination scope from a directory path, and the
relocation machinery deletes every child of the trash directory. Both would
follow a symlink or a junction planted where azd expects a real directory: the
kill scope would widen to the link target, and a sweep triggered by any ordinary
install or upgrade would delete the contents of whatever the link pointed at.
The sweep runs whether or not `--force` was passed, which makes it the more
reachable of the two.

`osutil.RequireRealDir` refuses a path whose final component is a link, and is
called before the trash directory is swept or relocated into, and before a
directory becomes a termination scope. azd creates all of these directories
itself, so a link is never something it produced.

Both reparse forms have to be tested, because on Windows they present
differently. Verified on Windows 11 through Go: a directory symlink sets
`ModeSymlink` and is resolved by `filepath.EvalSymlinks`, while a junction sets
`ModeIrregular`, reports `IsDir` false, and is not resolved by `EvalSymlinks` at
all, yet `os.ReadDir` and `os.Rename` still follow it. Checking only `ModeSymlink`
would therefore miss the case that needs no elevation to create.

`os.RemoveAll` is the exception and does not follow either form. It calls
`os.Remove` first, which succeeds against a reparse point because
`RemoveDirectory` detaches the link without touching its target, and its
recursive branch is gated on `os.Lstat(...).IsDir()`, which is false for both a
junction and a directory symlink. Verified on Windows 11 through Go: after
`os.RemoveAll` against a junction, the link is gone and every file under its
target survives. A planted link is therefore deleted as a link rather than
recursed into, which is why the sweep can hand each trash entry straight to
`os.RemoveAll`.

The check deliberately inspects only the final component. Resolving symlinked
ancestors is still required for correctness on macOS, where the temporary and
configuration directories sit under `/var` and the operating system reports
process executables under `/private/var`.

That is also why one call is not enough. A final-component check on
`<configDir>/extensions/<id>` passes when `<configDir>/extensions` is itself a
link, because the operating system resolves the link while walking to the final
component, and everything downstream then acts on the link's target: the
termination scope widens to it, the sweep deletes children there, and an install
writes there. `requireRealExtensionDirs` therefore checks both components azd
creates, the extensions root and the extension directory, and is called before an
uninstall sweeps, terminates, or removes, and immediately after install creates
the target directory. Only those two are checked, so symlinked ancestors above
the config directory stay legal and macOS keeps working.

### Scoping to the extension binary only

`--force` stops the extension process itself and not its descendants. Walking
the process tree would reach processes whose executables live outside the
extension directory, which is precisely where the containment guarantee stops
holding. A tree walk trades the one property that makes automated termination
defensible for the convenience of reaping orphans, and orphan cleanup is the
extension's own responsibility on shutdown. This is a deliberate trade: some
orphaned children may survive, and that is preferable to a code path that can
terminate something azd never installed.

### Graceful shutdown on Windows

On Unix, `Terminate` sends SIGTERM, waits out the grace period, then sends
SIGKILL. Windows skips the graceful step and goes directly to
`TerminateProcess`.

The only general mechanism Windows offers for asking a console-less child to
exit is `GenerateConsoleCtrlEvent`, and it targets a **process group**, not a
process. An extension started by azd shares azd's console, so the signal would
reach azd itself. Terminating the user's own CLI session to be polite to an
extension is a worse outcome than an abrupt extension exit, so Windows escalates
immediately.

The Windows `signalGraceful` therefore always returns an error, which
short-circuits the graceful branch before the grace period is ever waited. The
bound on how long a forced stop can take on Windows is consequently not the
grace period but the shorter post-`TerminateProcess` confirmation window, so
`--force` cannot hang on any platform.

### Error message when relocation also fails

When the operation still cannot complete, the error names the blocking
processes with PIDs and suggests `--force`, using `internal.ErrorWithSuggestion`
rather than surfacing a bare Win32 error.

### Flag surface

Both `--force` and its `-f` shorthand are registered on `upgrade` and
`uninstall`, matching the shorthand `install --force` already uses. Install's
`--force` additionally implies "stop running extension processes" when the
extension is already installed and install falls through to an upgrade, so the
single word behaves consistently across all three commands.

## Alternatives Considered

- **`--force` only, no relocation.** Leaves the default path broken. Every user
  hitting this still gets an access-denied failure and has to opt into
  terminating processes just to complete a routine upgrade. Making the common
  case require a destructive flag is the wrong default.
- **Relocation only, no `--force`.** Upgrade would stop failing, but the running
  process keeps serving old code with no supported way to make the new version
  take effect. Users would go back to hunting PIDs by hand, which is the
  original complaint.
- **Terminate the whole process tree.** Rejected above. The containment
  guarantee is the entire safety argument, and a tree walk voids it.
- **Longer or smarter retry in `osutil.RemoveAll`.** No retry window helps
  against a process that is running indefinitely. It converts a fast failure
  into a slow one.
- **Put process discovery in `pkg/azdext`.** That package is the extension SDK.
  Core azd importing it inverts the layering, and it would widen the public SDK
  compatibility surface for an internal need.
- **Put process discovery in `pkg/osutil`.** `osutil` is about files and paths.
  Process enumeration is a different concern with different platform code and
  different failure modes.
- **Reuse `azdext.FindProcessByName`.** It matches on executable name only,
  which could select an unrelated process that happens to share a name. Not
  safe for a code path that terminates what it finds.
- **Name the flag `--stop-running`.** More literal, but it fragments the CLI
  vocabulary and loses the muscle memory users already reach for.

## Risks & Rabbit Holes

- **Process query code is duplicated with `pkg/azdext`.** Accepted for now. The
  layering rule prevents core from importing the SDK, and inverting the
  dependency so the SDK consumes `processutil` is a larger refactor than this
  change should carry. Worth revisiting separately.
- **Graceful shutdown on Windows is not available.** Resolved during
  implementation and recorded above: Windows escalates straight to
  `TerminateProcess` because the alternative can signal azd's own console
  group. An extension that needs to flush state on exit cannot rely on being
  asked nicely on Windows.
- **Terminating a process does not immediately release its executable.**
  Windows tears down the image section slightly after the process object goes
  away, so a removal issued immediately after a confirmed exit can still fail
  and fall through to relocation. Uninstall therefore sweeps the trash
  directory again after a successful removal, so `--force` leaves nothing
  permanent behind.
- **Linux `/proc/<pid>/exe` appends ` (deleted)`** when the binary has been
  unlinked, which is exactly the state this feature creates. The suffix must be
  stripped or discovery silently misses the processes it exists to find.
- **macOS discovery reads `comm`, not `args`.** `ps -o args=` yields argv[0],
  which the launching process fully controls and which is not guaranteed to be a
  resolved path, so it is unusable as the basis for a termination decision.
  `pkg/azdext` uses it because it only ever reports; `processutil` uses
  `ps -axww -o comm=` instead, which reports the real executable path. The `-ww`
  is load-bearing: without it `ps` truncates output at terminal width, and
  because `comm` is the last column a truncated path silently fails containment
  and makes `--force` a no-op. `ps` is invoked by absolute path so `$PATH` cannot
  redirect it.
- **The relocation trash is out of reach of `--force`.** Relocated binaries land
  in `.trash`, a sibling of the extension directory, so a later
  `FindByExecutableDir(extensionDir)` cannot see a process still running from a
  previously relocated image. This is deliberate. Widening discovery to the trash
  would let one extension's `--force` terminate a process belonging to a
  different extension that happens to share the trash. The residue is bounded:
  the trash is swept on every install and every successful forced uninstall, so
  the entry disappears as soon as its process does.
- **Trash directory growth.** If a process never exits, its relocated binary is
  never removable. Sweeping is best-effort and must never fail the command it
  is attached to.
- **Do not try to solve** self-update of the `azd` binary itself, extension
  health checks, or a supervised extension lifecycle. All are adjacent and all
  are out of scope.

### Testing real processes

Windows file-lock and process-image behavior cannot be faked, so several tests
need a genuine process running from a directory the test chose. The technique is
to copy the running test binary into that directory and re-execute it in a
helper mode. Four packages needed it, so it lives once in `test/proctest`
alongside the repository's other non-test testing helpers (`test/azdcli`,
`test/ostest`, `test/snapshot`).

One detail worth recording: a process id lookup is not a liveness test. On
Windows the id stays resolvable while the `exec` package still holds a handle to
a terminated process, so `os.FindProcess` reports a dead process as alive.
`proctest` observes exits by waiting on the process, which is the only
authoritative answer.

### File layout

`pkg/extensions/manager.go` was already 1316 lines before this change, and the
uninstall path grew by roughly 400 more. Rather than push a file that is already
well past the repository's size guidance further out of reach, the uninstall
concern moved into `pkg/extensions/manager_uninstall.go`: the options type, the
three uninstall entry points, the path validator, process stopping, and the
blocked-removal error. It stays in the same package, so nothing about the public
surface or any import changes, and `manager.go` ends up only 224 lines larger
than it started.

### Accepted risks

Three residual concerns were raised during review and deliberately not coded
against, because in each case the mitigation already exists or the failure is
fail-safe. A later round added a fourth.

- **No explicit same-user check before terminating.** The operating system
  already enforces this. A non-elevated azd cannot obtain `PROCESS_TERMINATE` on
  another user's process on Windows, and `kill` returns `EPERM` across users on
  Unix. Adding an ownership lookup would duplicate a check the kernel performs
  anyway, and the process must additionally be executing a binary inside the
  invoking user's own configuration directory to have been discovered at all.
- **Unicode case folding may differ between `IsPathContained` and the
  filesystem.** Go's case-insensitive comparison and NTFS's upcase table are not
  byte-for-byte identical for exotic code points. A disagreement makes
  containment reject a path it might have accepted, so discovery returns fewer
  processes and `--force` under-reaches. It cannot cause a process outside the
  extension directory to be terminated, which is the property that matters.
- **Extended-length `\\?\` paths would not match a plain scope.**
  `QueryFullProcessImageName` is called with a zero flags argument, which
  requests Win32 path format and never returns the prefix, and
  `filepath.EvalSymlinks` does not introduce one for ordinary paths. If a
  prefixed path ever did appear, containment would fail closed and the process
  would simply not be stopped.
- **A residual PID-reuse window remains on Unix.** Windows closes it by pinning
  the process with an open handle before it validates and terminates. Linux could
  do the same with a pidfd and macOS has no equivalent, so on those platforms a
  target that exits between the live scope re-check and the signal, whose PID is
  then reissued inside that window, could be signalled. Exploiting it requires
  winning a race measured in microseconds against a PID the attacker does not
  choose, and the platform this feature exists for is the one where the window is
  closed.
