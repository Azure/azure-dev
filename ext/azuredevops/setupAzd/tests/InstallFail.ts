import * as path from 'path';
import * as assert from 'assert';
import * as ttm from 'azure-pipelines-task-lib/mock-test';
import * as fs from 'fs';

describe('setup azd tests - fails', function() {
    this.timeout(60000);
    before(function() { });
    afterEach(() => {
        fs.rmSync('path', { recursive: true, force: true })
    });

    it('should fail with invalid version', function(done: Mocha.Done) {
        this.timeout(10000);
        let tp = path.join(__dirname, 'invalidVersion.js');
        let tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);

        tr.runAsync().then(() => {
            assert.equal(tr.succeeded, false, 'should have failed');
            assert.equal(tr.warningIssues.length, 0, "should have no warnings");
            assert.equal(tr.errorIssues.length, 1, "should have error");
            assert.equal(tr.stdout.indexOf('Could not download from') >= 0, true, "should display error");
            done();
        }).catch((reason) => {
            done(reason);
        });
    });
});
