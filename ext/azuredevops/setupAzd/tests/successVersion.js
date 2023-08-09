"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const tmrm = require("azure-pipelines-task-lib/mock-run");
const path = require("path");
let taskPath = path.join(__dirname, '..', 'index.js');
let tmr = new tmrm.TaskMockRunner(taskPath);
tmr.setAnswers({
    cwd: { 'cwd': 'path' },
    which: { 'path/azd-install/azd': 'path' },
    checkPath: { 'path': true },
    exec: { 'path version': {
            code: 0,
            stdout: "mocked run"
        } }
});
tmr.setInput('version', '1.0.0');
tmr.run();
