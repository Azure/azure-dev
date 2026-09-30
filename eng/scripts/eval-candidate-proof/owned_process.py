# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Run a CLI inside an owned lifetime boundary, including its extension children."""

import ctypes
from ctypes import wintypes
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


class WindowsJob:
    def __init__(self):
        class Limits(ctypes.Structure):
            _fields_ = [
                ("process_time", ctypes.c_int64), ("job_time", ctypes.c_int64),
                ("flags", wintypes.DWORD), ("minimum_working_set", ctypes.c_size_t),
                ("maximum_working_set", ctypes.c_size_t), ("active_process_limit", wintypes.DWORD),
                ("affinity", ctypes.c_size_t), ("priority", wintypes.DWORD), ("scheduling", wintypes.DWORD),
            ]

        class IoCounters(ctypes.Structure):
            _fields_ = [(name, ctypes.c_uint64) for name in
                        ("read_ops", "write_ops", "other_ops", "read_bytes", "write_bytes", "other_bytes")]

        class ExtendedLimits(ctypes.Structure):
            _fields_ = [("basic", Limits), ("io", IoCounters), ("process_memory", ctypes.c_size_t),
                        ("job_memory", ctypes.c_size_t), ("peak_process_memory", ctypes.c_size_t),
                        ("peak_job_memory", ctypes.c_size_t)]

        self.kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        signatures = {
            "CreateJobObjectW": ([ctypes.c_void_p, wintypes.LPCWSTR], wintypes.HANDLE),
            "SetInformationJobObject": ([wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD], wintypes.BOOL),
            "AssignProcessToJobObject": ([wintypes.HANDLE, wintypes.HANDLE], wintypes.BOOL),
            "TerminateJobObject": ([wintypes.HANDLE, wintypes.UINT], wintypes.BOOL),
            "QueryInformationJobObject": ([wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD,
                                          ctypes.c_void_p], wintypes.BOOL),
            "CloseHandle": ([wintypes.HANDLE], wintypes.BOOL),
        }
        for name, (args, result) in signatures.items():
            function = getattr(self.kernel, name)
            function.argtypes, function.restype = args, result
        self.handle = self.kernel.CreateJobObjectW(None, None)
        if not self.handle:
            raise ctypes.WinError(ctypes.get_last_error())
        limits = ExtendedLimits()
        limits.basic.flags = 0x2000  # JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, with no breakaway.
        if not self.kernel.SetInformationJobObject(self.handle, 9, ctypes.byref(limits), ctypes.sizeof(limits)):
            error = ctypes.WinError(ctypes.get_last_error())
            self.close()
            raise error

    def assign(self, process):
        if not self.kernel.AssignProcessToJobObject(self.handle, int(process._handle)):
            raise ctypes.WinError(ctypes.get_last_error())

    def terminate(self):
        if not self.kernel.TerminateJobObject(self.handle, 1):
            raise ctypes.WinError(ctypes.get_last_error())

    def wait_empty(self, timeout):
        class Accounting(ctypes.Structure):
            _fields_ = [(name, ctypes.c_int64) for name in
                        ("user_time", "kernel_time", "period_user_time", "period_kernel_time")] + [
                            (name, wintypes.DWORD) for name in
                            ("page_faults", "total_processes", "active_processes", "terminated_processes")]

        deadline = time.monotonic() + timeout
        while True:
            info = Accounting()
            if not self.kernel.QueryInformationJobObject(
                    self.handle, 1, ctypes.byref(info), ctypes.sizeof(info), None):
                raise ctypes.WinError(ctypes.get_last_error())
            if info.active_processes == 0:
                return
            if time.monotonic() >= deadline:
                raise RuntimeError("Owned CLI process tree did not terminate within the cleanup bound")
            time.sleep(0.01)

    def close(self):
        if self.handle:
            handle, self.handle = self.handle, None
            if not self.kernel.CloseHandle(handle):
                raise ctypes.WinError(ctypes.get_last_error())


def run(args, *, cwd, env, timeout, text=False, encoding="utf-8"):
    started = time.monotonic()
    job = WindowsJob() if os.name == "nt" else None
    process = None
    assigned = False

    def terminate():
        if process is None:
            return
        if job is not None and assigned:
            job.terminate()
        elif os.name != "nt":
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        if process.poll() is None:
            process.kill()

    try:
        # The launcher cannot create children until its private stdin is supplied,
        # so Windows job assignment happens before any azd or extension code runs.
        process = subprocess.Popen(
            [sys.executable, "-I", str(Path(__file__).resolve())], cwd=cwd, env=env,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            start_new_session=os.name != "nt",
        )
        if job is not None:
            job.assign(process)
            assigned = True
        remaining = timeout - (time.monotonic() - started)
        if remaining <= 0:
            raise subprocess.TimeoutExpired(args, timeout)
        try:
            stdout, stderr = process.communicate(json.dumps({"argv": [str(arg) for arg in args]}).encode(),
                                                 timeout=remaining)
        except subprocess.TimeoutExpired:
            terminate()
            stdout, stderr = process.communicate(timeout=5)
            raise subprocess.TimeoutExpired(args, timeout, output=stdout, stderr=stderr) from None
        result = subprocess.CompletedProcess(args, process.returncode, stdout, stderr)
        if text:
            result.stdout, result.stderr = stdout.decode(encoding), stderr.decode(encoding)
        return result
    finally:
        try:
            terminate()
        finally:
            try:
                if process is not None:
                    process.wait(timeout=5)
                    for stream in (process.stdin, process.stdout, process.stderr):
                        if stream is not None:
                            stream.close()
                if job is not None and assigned:
                    job.wait_empty(5)
            finally:
                if job is not None:
                    job.close()


def main():
    request = json.load(sys.stdin)
    if (not isinstance(request, dict) or set(request) != {"argv"}
            or not isinstance(request["argv"], list) or not request["argv"]
            or not all(isinstance(arg, str) for arg in request["argv"])):
        raise ValueError("Invalid owned CLI launch request")
    return subprocess.run(request["argv"], stdin=subprocess.DEVNULL, check=False).returncode


if __name__ == "__main__":
    sys.exit(main())
