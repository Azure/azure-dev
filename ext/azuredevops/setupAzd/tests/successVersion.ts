import ma = require('azure-pipelines-task-lib/mock-answer');
import tmrm = require('azure-pipelines-task-lib/mock-run');
import path = require('path');

let taskPath = path.join(__dirname, '..', 'index.js');
let tmr: tmrm.TaskMockRunner = new tmrm.TaskMockRunner(taskPath);

tmr.setAnswers({
    cwd: {'cwd': 'path'},
    which: {'path/azd-install/azd': 'path'},
    checkPath: {'path': true},
    exec: {'path version': {
        code: 0,
        stdout: "mocked run"
    }}
});
tmr.setInput('version', '1.0.0');
tmr.run();
