import ma = require('azure-pipelines-task-lib/mock-answer');
import tmrm = require('azure-pipelines-task-lib/mock-run');
import os = require('os');
import path = require('path');

const taskPath = path.join(__dirname, '..', 'index.js');
const tmr: tmrm.TaskMockRunner = new tmrm.TaskMockRunner(taskPath);
const tempDirectory = '/tmp/setup-azd-test';
const installScriptPath = path.join(tempDirectory, 'install-azd.sh');

tmr.registerMock('os', {
    ...os,
    platform: () => 'linux',
});
tmr.registerMock('fs/promises', {
    mkdtemp: async () => tempDirectory,
    rm: async () => {
        throw new Error('directory is busy');
    },
});
tmr.setInput('version', 'stable');

const answers: ma.TaskLibAnswers = {
    which: {
        'bash': '/bin/bash',
        'curl': '/usr/bin/curl',
        'sudo': '/usr/bin/sudo',
    },
    checkPath: {
        '/bin/bash': true,
        '/usr/bin/curl': true,
        '/usr/bin/sudo': true,
    },
    exec: {
        [`/usr/bin/curl -fsSL https://aka.ms/install-azd.sh -o ${installScriptPath}`]: {
            code: 0,
            stdout: 'Downloaded azd installer',
        },
        [`/usr/bin/sudo /bin/bash ${installScriptPath} --version stable --verbose`]: {
            code: 0,
            stdout: 'azd installed successfully',
        },
    },
};

tmr.setAnswers(answers);
tmr.run();
