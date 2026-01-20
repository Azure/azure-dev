import * as path from 'path';
import * as assert from 'assert';
import * as ttm from 'azure-pipelines-task-lib/mock-test';

describe('Setup azd task tests', function () {

    before(function() {
        // Disable tool download for tests to prevent actual network calls
        process.env.AGENT_TOOLSDIRECTORY = '/tmp/test-tools';
        process.env.RUNNER_TOOL_CACHE = '/tmp/test-tools';
    });

    after(() => {
        // Cleanup after tests
        delete process.env.AGENT_TOOLSDIRECTORY;
        delete process.env.RUNNER_TOOL_CACHE;
    });

    it('should succeed with default version (latest)', function(done: Mocha.Done) {
        // Increased timeout for macOS where tool cache operations can take longer
        this.timeout(30000);

        const tp: string = path.join(__dirname, 'success.js');
        const tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);

        tr.runAsync().then(() => {
            assert.equal(tr.succeeded, true, 'should have succeeded');
            assert.equal(tr.warningIssues.length, 0, 'should have no warnings');
            assert.equal(tr.errorIssues.length, 0, 'should have no errors');
            assert.ok(tr.stdout.indexOf('Installing azd version latest') >= 0, 'should display installing latest version');
            done();
        }).catch((error) => {
            done(error);
        });
    });

    it('should succeed with specific version', function(done: Mocha.Done) {
        // Increased timeout for macOS compatibility
        this.timeout(30000);

        const tp: string = path.join(__dirname, 'successVersion.js');
        const tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);

        tr.runAsync().then(() => {
            assert.equal(tr.succeeded, true, 'should have succeeded');
            assert.equal(tr.warningIssues.length, 0, 'should have no warnings');
            assert.equal(tr.errorIssues.length, 0, 'should have no errors');
            assert.ok(tr.stdout.indexOf('Installing azd version 1.0.0') >= 0, 'should display installing version 1.0.0');
            done();
        }).catch((error) => {
            done(error);
        });
    });

    it('should fail with invalid version', function(done: Mocha.Done) {
        // Increased timeout for macOS compatibility
        this.timeout(30000);

        const tp: string = path.join(__dirname, 'invalidVersion.js');
        const tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);

        tr.runAsync().then(() => {
            assert.equal(tr.succeeded, false, 'should have failed');
            assert.equal(tr.warningIssues.length, 0, 'should have no warnings');
            assert.ok(tr.errorIssues.length > 0, 'should have at least one error');
            done();
        }).catch((error) => {
            done(error);
        });
    });
});
