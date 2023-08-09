import * as path from 'path';
import * as assert from 'assert';
import * as ttm from 'azure-pipelines-task-lib/mock-test';

describe('setup azd tests', function () {

    before( function() {});
    after(() => {});

    it('should succeed with empty version', function(done: Mocha.Done) {
        let tp = path.join(__dirname, 'success.js');
        let tr: ttm.MockTestRunner = new ttm.MockTestRunner(tp);
    
        tr.run();
        console.log(tr.succeeded);
        console.log(tr.stdout);
        assert.equal(tr.succeeded, true, 'should have succeeded');
        assert.equal(tr.warningIssues.length, 0, "should have no warnings");
        assert.equal(tr.errorIssues.length, 0, "should have no errors");
        console.log(tr.stdout);
        //assert.equal(tr.stdout.indexOf('using version') >= 0, true, "should display version");
        done();
    });

    // it('it should fail if tool returns 1', function(done: Mocha.Done) {
    //     // Add failure test here
    // });    
});
