import tmrm = require('azure-pipelines-task-lib/mock-run');
import os = require('os');
import path = require('path');

const taskPath = path.join(__dirname, '..', 'index.js');
const tmr: tmrm.TaskMockRunner = new tmrm.TaskMockRunner(taskPath);

tmr.registerMock('os', {
    ...os,
    platform: () => 'linux',
});
tmr.setInput('version', "latest'; Write-Output unexpected #");
tmr.run();
