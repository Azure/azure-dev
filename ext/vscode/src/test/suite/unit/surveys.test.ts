// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { assert } from 'chai';
import { activeSurveys } from "../../../telemetry/activeSurveys";
import { getSurveyFlightName } from "../../../telemetry/surveyScheduler";

const maxFlightNameLength = 32;
const conformingFlightNameMatcher: RegExp = /[a-z\-_]+/i;

suite('survey tests', () => {
    test('survey IDs meet requirements', () => {
        for (const survey of activeSurveys) {
            const flightName = getSurveyFlightName(survey);
            assert.isDefined(flightName, 'flight ID cannot be empty');
            assert.isNotEmpty(flightName);
            assert.isTrue(flightName.length <= maxFlightNameLength, `flight ID cannot exceed ${maxFlightNameLength} characters`);

            const results = conformingFlightNameMatcher.exec(flightName);
            assert.isNotNull(results, 'flight ID contains invalid characters (no match)');

            const match = (results as RegExpExecArray)[0];
            assert.lengthOf(match, flightName.length, 'flight ID contains invalid characters (partial match)');
        }
    });
});
