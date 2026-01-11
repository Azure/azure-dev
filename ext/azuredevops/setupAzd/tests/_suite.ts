import * as path from 'path';
import * as assert from 'assert';
import * as ttm from 'azure-pipelines-task-lib/mock-test';

describe('Setup azd task tests', function () {

    before(function() {
        // Setup before tests
    });

    after(() => {
        // Cleanup after tests
    });

    it('should succeed with default version (latest)', function(done: Mocha.Done) {
        this.timeout(5000);

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
        this.timeout(5000);

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
        this.timeout(5000);

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
