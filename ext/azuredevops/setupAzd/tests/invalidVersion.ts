import ma = require('azure-pipelines-task-lib/mock-answer');
import tmrm = require('azure-pipelines-task-lib/mock-run');
import path = require('path');

const taskPath = path.join(__dirname, '..', 'index.js');
const tmr: tmrm.TaskMockRunner = new tmrm.TaskMockRunner(taskPath);

// Set input with an invalid version
tmr.setInput('version', '1.9999999.0');

// Mock answers - simulate failure for invalid version (Windows and Linux/Mac)
const answers: ma.TaskLibAnswers = {
    which: {
        'powershell': 'C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe',
        'bash': '/bin/bash'
    },
    checkPath: {
        'C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe': true,
        '/bin/bash': true
    },
    exec: {
        // Windows PowerShell install command - fails with invalid version
        [`C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe -NoLogo -NoProfile -NonInteractive -Command $scriptPath = "$($env:TEMP)\\install-azd.ps1"; Invoke-RestMethod 'https://aka.ms/install-azd.ps1' -OutFile $scriptPath; . $scriptPath -Version '1.9999999.0' -Verbose:$true; Remove-Item $scriptPath`]: {
            code: 1,
            stdout: 'Could not download - version 1.9999999.0 not found'
        },
        // Linux/Mac bash install command - fails with invalid version
        '/bin/bash -c curl -fsSL https://aka.ms/install-azd.sh | sudo bash -s -- --version 1.9999999.0 --verbose': {
            code: 1,
            stdout: 'Could not download from https://aka.ms/install-azd.sh - version 1.9999999.0 not found'
        }
    }
};

tmr.setAnswers(answers);
tmr.run();
