import ma = require('azure-pipelines-task-lib/mock-answer');
import tmrm = require('azure-pipelines-task-lib/mock-run');
import path = require('path');

const taskPath = path.join(__dirname, '..', 'index.js');
const tmr: tmrm.TaskMockRunner = new tmrm.TaskMockRunner(taskPath);

// Set input for success scenario (empty version = latest)
tmr.setInput('version', '');

// Get the mocked LocalAppData path for Windows
const mockLocalAppData = process.env.LocalAppData || 'C:\\Users\\test\\AppData\\Local';
const azdExePath = `${mockLocalAppData}\\Programs\\Azure Dev CLI\\azd.exe`;

// Mock answers for tool lookups and executions (Windows and Linux/Mac)
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
        // Windows PowerShell install command
        [`C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe -NoLogo -NoProfile -NonInteractive -Command $scriptPath = "$($env:TEMP)\\install-azd.ps1"; Invoke-RestMethod 'https://aka.ms/install-azd.ps1' -OutFile $scriptPath; . $scriptPath -Version 'latest' -Verbose:$true; Remove-Item $scriptPath`]: {
            code: 0,
            stdout: 'azd installed successfully'
        },
        // Windows azd version check
        [`${azdExePath} version`]: {
            code: 0,
            stdout: 'azd version 1.0.0'
        },
        // Linux/Mac bash install command  
        '/bin/bash -c curl -fsSL https://aka.ms/install-azd.sh | sudo bash -s -- --version latest --verbose': {
            code: 0,
            stdout: 'azd installed successfully'
        }
    }
};

tmr.setAnswers(answers);
tmr.run();
