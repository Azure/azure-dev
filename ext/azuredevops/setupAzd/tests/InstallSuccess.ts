import * as path from 'path';
import * as assert from 'assert';
import * as ttm from 'azure-pipelines-task-lib/mock-test';
import * as fs from 'fs'

describe('setup azd tests', function () {
    setTimeout(() => {}, 10000);
    before(function () { });
    afterEach(() => {
        fs.rmSync('path', { recursive: true, force: true })
    });

    it('should succeed with empty version', async function () {
        this.timeout(10000);
        let tp = path.join(__dirname, 'success.js');
        let tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);
        await tr.runAsync();
        assert.equal(tr.succeeded, true, 'should have succeeded');
        assert.equal(tr.warningIssues.length, 0, "should have no warnings");
        assert.equal(tr.errorIssues.length, 0, "should have no errors");
        assert.equal(tr.stdout.indexOf('using version: latest') >= 0, true, "should display version");
    });

    it('should succeed with version', async function () {
        this.timeout(10000);
        let tp = path.join(__dirname, 'successVersion.js');
        let tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);
        
        await tr.runAsync();
        assert.equal(tr.succeeded, true, 'should have succeeded');
        assert.equal(tr.warningIssues.length, 0, "should have no warnings");
        assert.equal(tr.errorIssues.length, 0, "should have no errors");
        assert.equal(tr.stdout.indexOf('using version: 1.0.0') >= 0, true, "should display version");
    });
});
