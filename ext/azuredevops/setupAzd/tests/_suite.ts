import * as path from 'path';
import * as assert from 'assert';
import * as ttm from 'azure-pipelines-task-lib/mock-test';
import { isValidVersion } from '../version';

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
            assert.ok(
                tr.errorIssues.some((issue) => issue.includes('Failed to install azd. Exit code: 1')),
                'should report the installer exit code',
            );
            done();
        }).catch((error) => {
            done(error);
        });
    });

    it('should reject an invalid version format', function(done: Mocha.Done) {
        this.timeout(30000);

        const tp: string = path.join(__dirname, 'invalidVersionFormat.js');
        const tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);

        tr.runAsync().then(() => {
            assert.equal(tr.succeeded, false, 'should have failed');
            assert.equal(tr.warningIssues.length, 0, 'should have no warnings');
            assert.ok(tr.errorIssues.some((issue) => issue.includes('Version must be')), 'should report an invalid version');
            assert.equal(tr.stdout.indexOf('Installing azd version'), -1, 'should fail before installation');
            done();
        }).catch((error) => {
            done(error);
        });
    });

    it('should accept supported version formats', function() {
        const versions = [
            'latest',
            'stable',
            'daily',
            '1.0.0',
            '1.14.0-beta.1',
            '1.14.0-beta.1+build.5',
        ];

        for (const version of versions) {
            assert.equal(isValidVersion(version), true, `should accept ${version}`);
        }
    });

    it('should reject unsupported version formats', function() {
        const versions = [
            '01.2.3',
            '1.02.3',
            '1.2.03',
            '1.2.3-01',
            '1.2',
            '1.2.3.4',
            'latest ',
            'latest\n',
        ];

        for (const version of versions) {
            assert.equal(isValidVersion(version), false, `should reject ${version}`);
        }
    });

    it('should warn when installer cleanup fails', function(done: Mocha.Done) {
        this.timeout(30000);

        const tp: string = path.join(__dirname, 'cleanupFailure.js');
        const tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);

        tr.runAsync().then(() => {
            assert.equal(tr.succeeded, true, 'should have succeeded');
            assert.equal(tr.errorIssues.length, 0, 'should have no errors');
            assert.ok(
                tr.warningIssues.some((issue) => issue.includes('Failed to clean up installer files')),
                'should report the cleanup warning',
            );
            done();
        }).catch((error) => {
            done(error);
        });
    });

    it('should preserve installer errors when cleanup also fails', function(done: Mocha.Done) {
        this.timeout(30000);

        const tp: string = path.join(__dirname, 'installAndCleanupFailure.js');
        const tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);

        tr.runAsync().then(() => {
            assert.equal(tr.succeeded, false, 'should have failed');
            assert.ok(
                tr.errorIssues.some((issue) => issue.includes('Failed to install azd. Exit code: 1')),
                'should preserve the installer error',
            );
            assert.ok(
                tr.warningIssues.some((issue) => issue.includes('Failed to clean up installer files')),
                'should report the cleanup warning',
            );
            done();
        }).catch((error) => {
            done(error);
        });
    });

    it('should report download failures with the exit code', function(done: Mocha.Done) {
        this.timeout(30000);

        const tp: string = path.join(__dirname, 'downloadFailure.js');
        const tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);

        tr.runAsync().then(() => {
            assert.equal(tr.succeeded, false, 'should have failed');
            assert.equal(tr.warningIssues.length, 0, 'should have no warnings');
            assert.ok(
                tr.errorIssues.some((issue) => issue.includes('Failed to download the azd installer. Exit code: 1')),
                'should report the download exit code',
            );
            done();
        }).catch((error) => {
            done(error);
        });
    });
});
