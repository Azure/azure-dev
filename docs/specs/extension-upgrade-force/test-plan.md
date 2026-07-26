# Test Plan: Unblocking Extension Upgrade and Uninstall When the Extension Is Running

## Status: COVERED
## Spec: docs/specs/extension-upgrade-force/spec.md
## Issue: https://github.com/Azure/azure-dev/issues/9307
## Created: 2026-07-25
## Updated: 2026-07-25

---

## Coverage Strategy

Go standard testing with `testify`, matching existing repository conventions.

| Level | Applies to | Why |
|---|---|---|
| Unit | `pkg/processutil` safety guards, error message construction, flag binding | Path containment is the load-bearing safety property and is pure logic, so it must be exhaustively tested without spawning anything |
| Integration | Real process spawn and terminate, real locked-file relocation | Windows file-lock and process-image semantics cannot be faked meaningfully. These paths must run against the real OS |
| Snapshot | CLI help and figspec output | Repository already gates flag surface changes on snapshots |

Commands:

```
go test ./pkg/processutil/... ./pkg/osutil/... ./pkg/extensions/... ./cmd/...
UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'
```

Coverage targets: at least 80 percent of new and modified lines overall, and at
least 90 percent for `pkg/processutil` path scoping and exclusion logic, which
is the code that decides whether a process gets terminated.

Platform-specific tests are gated with build tags or `t.Skip` and must be run on
Windows, macOS, and Linux. Windows is the primary platform for this feature and
the only one where the default path is currently broken, so Windows integration
tests are mandatory before ship.

## Planned Tests

Every planned test is automated. Test files use short package-relative paths rooted
at `cli/azd/`.

| ID | Behavior to verify | Source | Level | Test file -> name | Status |
|----|--------------------|--------|-------|-------------------|--------|
| T1 | Empty directory argument is rejected rather than matching everything | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_RejectsEmptyDir` | automated |
| T2 | Unix root `/` is rejected | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_RejectsUnixRoot` | automated |
| T3 | Windows drive root `C:\` is rejected | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_RejectsWindowsRoot` | automated |
| T4 | UNC and drive-relative roots are rejected | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_RejectsUNCRoot` | automated |
| T5 | The current azd process is never returned as a candidate | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_ExcludesSelf` | automated |
| T6 | Symlinked install directory resolves before containment comparison | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_ResolvesSymlinks` | automated |
| T7 | Relative directory input is resolved to absolute before comparison | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_ResolvesRelativePath` | automated |
| T8 | Sibling directory sharing a name prefix is not matched | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_RejectsPrefixSibling` | automated |
| T9 | Containment is case-insensitive on Windows and case-sensitive on Linux | AC-5, AC-9 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_CaseSensitivityByPlatform` | automated |
| T10 | A directory that does not exist yields no matches and no panic | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_MissingDir` | automated |
| T11 | A real spawned process inside the directory is discovered with correct PID, name, and executable | AC-3, AC-9 | integration | `pkg/processutil/processutil_integration_test.go` -> `TestFindByExecutableDir_FindsSpawnedProcess` | automated |
| T12 | A real process outside the directory is not discovered | AC-5 | integration | `pkg/processutil/processutil_integration_test.go` -> `TestFindByExecutableDir_IgnoresOutsideProcess` | automated |
| T13 | Linux `(deleted)` suffix on `/proc/<pid>/exe` is stripped so unlinked binaries still match | AC-9 | unit | `pkg/processutil/processutil_procfs_test.go` -> `TestNormalizeProcExe_StripsDeletedSuffix` | automated |
| T14 | Enumeration tolerates per-PID permission denials and keeps scanning | AC-9 | integration | `pkg/processutil/processutil_integration_test.go` -> `TestFindByExecutableDir_TolerantOfDeniedPids` | automated |
| T15 | Terminate stops a cooperating process within the grace period | AC-3 | integration | `pkg/processutil/processutil_integration_test.go` -> `TestTerminate_GracefulStop` | automated |
| T16 | Terminate escalates to a forceful stop when the grace period expires | AC-3 | integration | `pkg/processutil/processutil_integration_test.go` -> `TestTerminate_EscalatesToForce` | automated |
| T17 | Terminate on an already-exited PID is not an error | AC-3 | integration | `pkg/processutil/processutil_integration_test.go` -> `TestTerminate_AlreadyExited` | automated |
| T18 | Terminate honors context cancellation and never blocks past the bounded grace | AC-8 | integration | `pkg/processutil/processutil_integration_test.go` -> `TestTerminate_BoundedByContext` | automated |
| T19 | A locked file is relocated into the trash directory outside the extension directory | AC-1 | integration | `pkg/osutil/relocate_test.go` -> `TestRelocateLocked_MovesFileOutOfDir` | automated |
| T20 | Removal of a directory containing a locked executable succeeds via the relocation fallback | AC-1, AC-2 | integration | `pkg/osutil/relocate_test.go` -> `TestRemoveAllWithRelocation_LockedExe` | automated |
| T21 | Relocation is a no-op when nothing in the directory is locked | AC-1 | unit | `pkg/osutil/relocate_test.go` -> `TestRemoveAllWithRelocation_NoLocks` | automated |
| T22 | Sweep deletes trash entries once the holding process has exited | AC-11 | integration | `pkg/osutil/relocate_test.go` -> `TestSweepTrash_RemovesReleasedEntries` | automated |
| T23 | Sweep leaves still-locked trash entries in place without erroring | AC-11 | integration | `pkg/osutil/relocate_test.go` -> `TestSweepTrash_SkipsLockedEntries` | automated |
| T24 | Sweep failure never propagates as a command failure | AC-11 | unit | `pkg/osutil/relocate_test.go` -> `TestSweepTrash_BestEffortNeverErrors` | automated |
| T25 | Uninstall succeeds when the extension directory holds a locked executable | AC-2 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestUninstall_SucceedsWithLockedBinary` | automated |
| T26 | Uninstall with Force terminates discovered extension processes before removal | AC-4 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestUninstall_ForceTerminatesProcesses` | automated |
| T27 | Upgrade propagates Force through to the uninstall step | AC-3 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestUpgrade_PropagatesForce` | automated |
| T28 | Without Force, a blocked removal produces an error naming processes and PIDs and suggesting `--force` | AC-7 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestUninstall_BlockedErrorNamesProcesses` | automated |
| T29 | With Force unset, no termination is ever attempted | AC-1, AC-5 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestUninstall_NoForceNeverTerminates` | automated |
| T30 | Stopped processes are reported to the user by name and PID | AC-6 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestUninstall_ForceTerminatesProcesses` | automated |
| T31 | `--force` is registered on `azd extension upgrade` and bound to the upgrade options | AC-3, AC-12 | unit | `cmd/extension_force_test.go` -> `TestExtensionUpgradeFlags_Force` | automated |
| T32 | `--force` is registered on `azd extension uninstall` and bound to the uninstall options | AC-4, AC-12 | unit | `cmd/extension_force_test.go` -> `TestExtensionUninstallFlags_Force` | automated |
| T33 | `--force` parses alongside `--all` on both commands | AC-10 | unit | `cmd/extension_force_test.go` -> `TestExtensionUpgradeFlags_ForceComposesWithOtherFlags`, `TestExtensionUninstallFlags_ForceComposesWithAll` | automated |
| T34 | `--force` performs no prompting and is deterministic end to end | AC-8 | integration | `cmd/extension_force_test.go` -> `TestExtensionUninstallAction_ForceStopsProcessesWithoutPrompting` | automated |
| T35 | Usage and figspec snapshots reflect the new flag | AC-12 | snapshot | `cmd` -> `TestUsage`, `TestFigSpec` | automated |

### Additional tests written beyond the plan

Written because implementation exposed behavior the plan did not anticipate.

| ID | Behavior to verify | Source | Level | Test file -> name | Status |
|----|--------------------|--------|-------|-------------------|--------|
| T36 | Filesystem root detection is correct for Unix, Windows drive, UNC, and relative forms | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestIsFilesystemRoot` | automated |
| T37 | Terminate refuses to target the azd process itself | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestTerminate_RefusesSelf` | automated |
| T38 | Terminate rejects a non-positive PID rather than signalling a process group | AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestTerminate_RejectsInvalidPID` | automated |
| T39 | Process rendering names the executable and PID, and degrades safely when unknown | AC-6 | unit | `pkg/processutil/processutil_test.go` -> `TestProcessInfoString` | automated |
| T40 | Multi-process descriptions read as a single user-facing list | AC-6, AC-7 | unit | `pkg/processutil/processutil_test.go` -> `TestDescribe` | automated |
| T41 | A zero wait timeout reports liveness without blocking | AC-8 | integration | `pkg/processutil/processutil_integration_test.go` -> `TestWaitForExit_ZeroTimeout` | automated |
| T42 | Removing a path that does not exist succeeds rather than erroring | AC-2 | unit | `pkg/osutil/relocate_test.go` -> `TestRemoveAllWithRelocation_MissingPath` | automated |
| T43 | A trash directory nested inside the directory being removed is rejected | AC-11 | unit | `pkg/osutil/relocate_test.go` -> `TestRemoveAllWithRelocation_RejectsTrashInsidePath` | automated |
| T44 | Both path arguments are required | AC-11 | unit | `pkg/osutil/relocate_test.go` -> `TestRemoveAllWithRelocation_RequiresPaths` | automated |
| T45 | Trash destinations never collide across repeated relocations | AC-11 | unit | `pkg/osutil/relocate_test.go` -> `TestUniqueTrashPath` | automated |
| T46 | Existence probing distinguishes missing from present without masking errors | AC-11 | unit | `pkg/osutil/relocate_test.go` -> `TestPathExists` | automated |
| T47 | A blocked removal with no discoverable processes still explains itself | AC-7 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestUninstall_BlockedErrorWithoutProcesses` | automated |
| T48 | Process stopping refuses an unscoped or root directory at the manager boundary | AC-5 | unit | `pkg/extensions/manager_uninstall_test.go` -> `TestStopExtensionProcesses_RejectsUnscopedDirectory` | automated |
| T49 | Stopping processes in a directory with none running is a no-op, not an error | AC-4 | unit | `pkg/extensions/manager_uninstall_test.go` -> `TestStopExtensionProcesses_NoProcesses` | automated |
| T50 | Upgrade completes while the extension is running and the new version lands on disk | AC-1, AC-2 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestUpgrade_SucceedsWhileExtensionIsRunning` | automated |
| T51 | Uninstall without `--force` leaves the running process alone at the command layer | AC-1 | integration | `cmd/extension_force_test.go` -> `TestExtensionUninstallAction_WithoutForceLeavesProcessRunning` | automated |
| T52 | Nothing stopped means nothing reported, so routine runs stay quiet | AC-6 | unit | `cmd/extension_force_test.go` -> `TestReportStoppedProcesses_SilentWhenNothingStopped` | automated |
| T53 | Every stopped process is named individually in the report | AC-6 | unit | `cmd/extension_force_test.go` -> `TestReportStoppedProcesses_NamesEachStoppedProcess` | automated |
| T54 | `upgrade --force` reaches the manager and stops the running extension | AC-1, AC-3 | integration | `cmd/extension_force_test.go` -> `TestExtensionUpgradeAction_ForceStopsRunningProcess` | automated |
| T55 | `upgrade` without `--force` leaves the process running | AC-1 | integration | `cmd/extension_force_test.go` -> `TestExtensionUpgradeAction_WithoutForceLeavesProcessRunning` | automated |
| T56 | `install --force` over a running install stops the process on the reinstall path | AC-3 | integration | `cmd/extension_force_test.go` -> `TestExtensionInstallAction_ForceStopsRunningProcessOnReinstall` | automated |
| T57 | Windows `signalGraceful` reports failure so termination escalates instead of waiting | AC-4 | unit | `pkg/processutil/processutil_windows_test.go` -> `TestSignalGraceful_IsUnsupportedOnWindows` | automated |
| T58 | Uninstall sweeps trash left behind by an earlier run | AC-11 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestUninstall_SweepsPreexistingTrash` | automated |
| T59 | Install sweeps trash left behind by an earlier run | AC-11 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestInstall_SweepsPreexistingTrash` | automated |
| T60 | `extensionPaths` rejects traversal, nesting, empty, dot, and the reserved trash name | AC-5 | unit | `pkg/extensions/manager_uninstall_test.go` -> `TestExtensionPaths_RejectsUnsafeIds` | automated |
| T61 | `extensionPaths` accepts an ordinary namespaced id | AC-5 | unit | `pkg/extensions/manager_uninstall_test.go` -> `TestExtensionPaths_AcceptsNormalId` | automated |
| T62 | `--force` reaches a dependency upgrade, not just the parent | AC-1, AC-3 | integration | `pkg/extensions/manager_uninstall_test.go` -> `TestUpgrade_PropagatesForceToDependencies` | automated |
| T63 | `--force` stops every extension in an `--all` run, not just the first | AC-10 | integration | `cmd/extension_force_test.go` -> `TestExtensionUninstallAction_ForceComposesWithAllAcrossExtensions` | automated |
| T64 | `RequireRealDir` accepts a real directory and a missing path, refuses a file and a directory link | AC-5 | unit | `pkg/osutil/relocate_test.go` -> `TestRequireRealDir` | automated |
| T65 | A link planted at the trash directory is refused, leaving the target's contents intact | AC-5, AC-11 | unit | `pkg/osutil/relocate_test.go` -> `TestSweepTrash_RefusesLinkedDirectory` | automated |
| T66 | Relocation refuses a linked trash directory rather than renaming files through it | AC-5, AC-11 | integration | `pkg/osutil/relocate_test.go` -> `TestRelocateLocked_RefusesLinkedTrash` | automated |
| T67 | A scope reached through a symlinked ancestor still resolves, so macOS containment keeps working | AC-2 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_ResolvesSymlinkedAncestors` | automated |
| T68 | A scope whose own final component is a link is refused by both normalization and discovery | AC-2, AC-5 | unit | `pkg/processutil/processutil_test.go` -> `TestFindByExecutableDir_RejectsLinkedScope` | automated |
| T69 | Ids that Windows aliases away (trailing dot or space), device names, and ids carrying `:` or control characters are rejected | AC-5 | unit | `pkg/extensions/manager_uninstall_test.go` -> `TestExtensionPaths_RejectsUnsafeIds` | automated |
| T70 | An aliased id resolves onto the directory a different extension owns, and is refused | AC-5 | unit | `pkg/extensions/manager_uninstall_test.go` -> `TestExtensionPaths_AliasedIdWouldResolveOntoAnotherExtension` | automated |

## Functionality Inventory (Phase 3 reconciliation)

Every unit of functionality in the diff, mapped to the test that covers it. Built by
enumerating declarations in the new files and the added declarations in the modified
files, then walking each back to an assertion.

| # | Functionality introduced | Location | Covered by | Status |
|---|--------------------------|----------|------------|--------|
| 1 | `ProcessInfo` carries PID, name, and executable path | `pkg/processutil/processutil.go` | T11, T39 | COVERED |
| 2 | `ProcessInfo.String` renders a process for a human | `pkg/processutil/processutil.go` | T39 | COVERED |
| 3 | `Describe` renders a set of processes as one list | `pkg/processutil/processutil.go` | T40 | COVERED |
| 4 | `FindByExecutableDir` returns only processes running from a directory | `pkg/processutil/processutil.go` | T5, T11, T12, T14 | COVERED |
| 5 | `Terminate` ends a process, gracefully first where the platform allows | `pkg/processutil/processutil.go` | T15, T16, T18 | COVERED |
| 6 | `Terminate` refuses to target azd itself or a non-positive PID | `pkg/processutil/processutil.go` | T37, T38 | COVERED |
| 7 | `normalizeScope` resolves symlinks and rejects empty input | `pkg/processutil/processutil.go` | T1, T6, T7, T10 | COVERED |
| 8 | `isFilesystemRoot` blocks a whole-volume scope | `pkg/processutil/processutil.go` | T2, T3, T36 | COVERED |
| 9 | `executableInScope` containment, including case rules and prefix traps | `pkg/processutil/processutil.go` | T8, T9, T12 | COVERED |
| 10 | `waitForExit` confirms exit within a budget without blocking forever | `pkg/processutil/processutil.go` | T18, T41 | COVERED |
| 11 | `ErrEmptyDirectory` and `ErrRootDirectory` sentinels | `pkg/processutil/processutil.go` | T1, T2, T3, T4, T48 | COVERED |
| 12 | Windows `enumerateProcesses` via `CreateToolhelp32Snapshot` | `pkg/processutil/processutil_windows.go` | T11, T12, T14 | COVERED |
| 13 | Windows `processImagePath` resolves a PID to its image | `pkg/processutil/processutil_windows.go` | T11 | COVERED |
| 14 | Windows `forceKill` and `processRunning` | `pkg/processutil/processutil_windows.go` | T15, T16, T17 | COVERED |
| 15 | Windows `signalGraceful` is a deliberate no-op that reports failure | `pkg/processutil/processutil_windows.go` | T57 | COVERED |
| 16 | Unix `signalGraceful` sends SIGTERM | `pkg/processutil/processutil_unix.go` | T15, T18 | COVERED |
| 17 | Unix `forceKill` escalates to SIGKILL after the grace period | `pkg/processutil/processutil_unix.go` | T16 | COVERED |
| 18 | Unix `processRunning` probes with signal 0 | `pkg/processutil/processutil_unix.go` | T17 | COVERED |
| 19 | Linux `enumerateProcesses` reads `/proc` | `pkg/processutil/processutil_procfs.go` | T11, T12 | COVERED |
| 20 | Linux `normalizeProcExe` strips the ` (deleted)` suffix | `pkg/processutil/processutil_procfs.go` | T13 | COVERED |
| 21 | Darwin `enumerateProcesses` shells out to `ps` | `pkg/processutil/processutil_darwin.go` | T11, T12 | COVERED |
| 22 | `RemoveAllWithRelocation` removes normally, then relocates what is locked | `pkg/osutil/relocate.go` | T19, T20, T21 | COVERED |
| 23 | `RemoveAllWithRelocation` tolerates a missing path | `pkg/osutil/relocate.go` | T42 | COVERED |
| 24 | `RemoveAllWithRelocation` validates its arguments and trash placement | `pkg/osutil/relocate.go` | T43, T44 | COVERED |
| 25 | `SweepTrash` best-effort deletes what is no longer locked | `pkg/osutil/relocate.go` | T22, T23, T24 | COVERED |
| 26 | `relocateLockedFiles` renames rather than deletes | `pkg/osutil/relocate.go` | T19, T20 | COVERED |
| 27 | `uniqueTrashPath` never collides | `pkg/osutil/relocate.go` | T45 | COVERED |
| 28 | `pathExists` distinguishes missing from present | `pkg/osutil/relocate.go` | T46 | COVERED |
| 29 | `UninstallOptions` carries `Force` and the stop callback | `pkg/extensions/manager_uninstall.go` | T25, T26, T29 | COVERED |
| 30 | `Uninstall` succeeds against a running extension by default | `pkg/extensions/manager_uninstall.go` | T25 | COVERED |
| 31 | `Uninstall` with `Force` stops processes first | `pkg/extensions/manager_uninstall.go` | T26, T29 | COVERED |
| 32 | `Uninstall` sweeps trash before and after removal | `pkg/extensions/manager_uninstall.go` | T30, T58 | COVERED |
| 33 | `stopExtensionProcesses` refuses an unscoped directory | `pkg/extensions/manager_uninstall.go` | T48 | COVERED |
| 34 | `stopExtensionProcesses` is a no-op when nothing is running | `pkg/extensions/manager_uninstall.go` | T49 | COVERED |
| 35 | `extensionRemovalError` explains a blocked removal and suggests a fix | `pkg/extensions/manager_uninstall.go` | T28, T47 | COVERED |
| 36 | `UpgradeOptions.Force` and callback propagate into `Uninstall` | `pkg/extensions/manager.go` | T27, T50 | COVERED |
| 37 | `installInternal` sweeps trash when recreating the install directory | `pkg/extensions/manager.go` | T59 | COVERED |
| 38 | `--force`/`-f` registered and bound on `extension upgrade` | `cmd/extension.go` | T31, T33 | COVERED |
| 39 | `--force`/`-f` registered and bound on `extension uninstall` | `cmd/extension.go` | T32 | COVERED |
| 40 | Uninstall action passes `Force` through and reports what it stopped | `cmd/extension.go` | T34, T51 | COVERED |
| 41 | Upgrade action passes `Force` through and reports what it stopped | `cmd/extension.go` | T54, T55 | COVERED |
| 42 | Install `--force` also stops processes on the already-installed path | `cmd/extension.go` | T56 | COVERED |
| 43 | `reportStoppedProcesses` names each process and stays silent otherwise | `cmd/extension.go` | T52, T53 | COVERED |
| 44 | Help and fig output reflect the new flags | `cmd/testdata/*` | T35 | COVERED |
| 45 | `extensionPaths` rejects any id that is not a single directory component | `pkg/extensions/manager_uninstall.go` | T60, T61 | COVERED |
| 46 | `--force` propagates from a parent upgrade into its dependency upgrades | `pkg/extensions/manager.go` | T62 | COVERED |
| 47 | `--force` applies to every extension expanded by `--all` | `cmd/extension.go` | T63 | COVERED |
| 48 | `validateExtensionId` rejects ids Windows aliases onto a different directory, device names, separators, and control characters | `pkg/extensions/manager_uninstall.go` | T69, T70 | COVERED |
| 49 | `RequireRealDir` refuses a symlink or junction while accepting a real or missing directory | `pkg/osutil/relocate.go` | T64 | COVERED |
| 50 | Trash sweeping and relocation refuse a linked trash directory | `pkg/osutil/relocate.go` | T65, T66 | COVERED |
| 51 | A termination scope refuses a linked directory while still resolving linked ancestors | `pkg/processutil/processutil.go` | T67, T68 | COVERED |

`test/proctest` is test infrastructure rather than shipped functionality. It has no tests
of its own by design; it is exercised by every test in rows 4, 5, 22, 30, 31, and 40 to 42,
and a defect in it fails those directly.

## Gaps & Additions

Reconciliation found five gaps, and a later adversarial review found three more. The first two
gaps are at the command layer and share a shape: a field
was threaded into `UninstallOptions` but the parallel command surfaces were not proven to
pass it. Flag-binding tests (T31, T33) would have stayed green even if the action had
never read the flag. The next three came from auditing the inventory itself, and one
of them (G-5) was a real product defect rather than a missing test. The last three (G-7 to
G-9) are security defects found by attacking the finished implementation rather than
reading it, and all three were real.

| Gap | Detail | Resolution |
|-----|--------|------------|
| G-1 | Upgrade action wiring of `Force` and `OnProcessStopped` into `UpgradeOptions` was unverified | Added T54 and T55 |
| G-2 | Install action wiring of `--force` on the already-installed path was unverified | Added T56 |
| G-3 | Rows 15, 32, and 37 named tests that exercised the code incidentally rather than asserting the behavior | Added T57, T58, and T59, which assert each directly |
| G-4 | `extensionPaths` became the single validator for every extension path but had no tests of its own | Added T60 and T61 |
| G-5 | `--force` was silently dropped when an upgrade recursed into a dependency | Fixed in `childOpts` and locked down by T62 |
| G-6 | AC-10 was only covered at the flag-parsing level, so a `--all` run that stopped the first extension and skipped the rest would have passed | Added T63, which runs two live extensions through one `--all --force` command |
| G-7 | A junction planted at `.trash` redirected sweeping and relocation, and the sweep runs on every install and upgrade even without `--force` | Added `RequireRealDir` and guarded both paths; T64 to T66 |
| G-8 | `normalizeScope` resolved a symlinked scope, so a link at the install directory widened the set of processes `--force` would terminate | Guarded the final component while still resolving ancestors; T67 and T68 |
| G-9 | Windows strips trailing dots and spaces, so `.trash.` bypassed the reserved-name check and `foo.` resolved onto another extension's directory | Added `validateExtensionId` ahead of every join; T69 and T70 |

Zero `GAP` rows remain.
