import ma = require('azure-pipelines-task-lib/mock-answer');
import taskMock = require('azure-pipelines-task-lib/mock-run');
import path = require('path');

let taskPath = path.join(__dirname, '..', 'index.js');
let tmr: taskMock.TaskMockRunner = new taskMock.TaskMockRunner(taskPath);

tmr.setAnswers({
    cwd: { 'cwd': 'path' },
    which: { 'path/azd-install/azd': 'path' },
    checkPath: { 'path': true },
    exec: {
        'path version': {
            code: 0,
            stdout: "mocked run"
        }
    }
});
tmr.setInput('version', '1.9999999.0');
tmr.run();
