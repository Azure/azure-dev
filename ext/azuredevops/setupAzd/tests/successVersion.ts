import ma = require('azure-pipelines-task-lib/mock-answer');
import tmrm = require('azure-pipelines-task-lib/mock-run');
import os = require('os');
import path = require('path');

const taskPath = path.join(__dirname, '..', 'index.js');
const tmr: tmrm.TaskMockRunner = new tmrm.TaskMockRunner(taskPath);
const tempDirectory = '/tmp/setup-azd-test';
const installScriptPath = path.join(tempDirectory, 'install-azd.ps1');

tmr.registerMock('os', {
    ...os,
    platform: () => 'win32',
});
tmr.registerMock('fs/promises', {
    mkdtemp: async () => tempDirectory,
    rm: async () => undefined,
});

// Set input for success with specific version
tmr.setInput('version', '1.0.0');

// Get the mocked LocalAppData path for Windows
const mockLocalAppData = process.env.LocalAppData || 'C:\\Users\\test\\AppData\\Local';
process.env.LocalAppData = mockLocalAppData;
const azdExePath = `${mockLocalAppData}\\Programs\\Azure Dev CLI\\azd.exe`;

// Mock answers for tool lookups and executions
const answers: ma.TaskLibAnswers = {
    which: {
        'powershell': 'C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe',
    },
    checkPath: {
        'C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe': true,
    },
    exec: {
        [`C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe -NoLogo -NoProfile -NonInteractive -Command $ErrorActionPreference = 'Stop'; Invoke-RestMethod -Uri 'https://aka.ms/install-azd.ps1' -OutFile $env:AZD_INSTALL_SCRIPT`]: {
            code: 0,
            stdout: 'Downloaded azd installer',
        },
        [`C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe -NoLogo -NoProfile -NonInteractive -File ${installScriptPath} -Version 1.0.0 -Verbose`]: {
            code: 0,
            stdout: 'azd version 1.0.0 installed successfully',
        },
        [`${azdExePath} version`]: {
            code: 0,
            stdout: 'azd version 1.0.0',
        },
    },
};

tmr.setAnswers(answers);
tmr.run();
