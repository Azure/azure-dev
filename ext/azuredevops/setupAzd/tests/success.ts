import ma = require('azure-pipelines-task-lib/mock-answer');
import tmrm = require('azure-pipelines-task-lib/mock-run');
import path = require('path');
import * as fs from 'fs'

let taskPath = path.join(__dirname, '..', 'index.js');
let tmr: tmrm.TaskMockRunner = new tmrm.TaskMockRunner(taskPath);

tmr.setAnswers({
    cwd: {'cwd': 'path'},
    which: {'path/azd-install/azd': 'path'},
    checkPath: {'path': false}
});
tmr.registerMock('fs', {
    symlinkSync: (target: fs.PathLike, path: fs.PathLike, type?: fs.symlink.Type | null | undefined)=> {
    },
    ReadStream: fs.ReadStream
});
tmr.setInput('version', '');
tmr.run();
