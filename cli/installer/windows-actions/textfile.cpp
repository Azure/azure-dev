// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

#include "pch.h"
#include <fileutil.h>
#include <strutil.h>

const UINT msidberrCustomActionDataUndefined = 25000;
const UINT msidberrCustomActionDataInvalid = 25001;
const UINT msidberrFileWriteFailed = 25002;

extern "C" UINT WINAPI WriteTextFile(__in MSIHANDLE hSession)
{
    HRESULT hr = S_OK;
    LPWSTR pwzCustomActionData = NULL;
    LPWSTR* rgwzArgs = NULL;
    UINT cArgs = 0;
    PMSIHANDLE hRecord;

    hr = WcaInitialize(hSession, __FUNCTION__);
    ExitOnFailure(hr, "failed to initialize");

    hr = WcaGetProperty(L"CustomActionData", &pwzCustomActionData);
    MessageExitOnFailure(hr, msidberrCustomActionDataUndefined, "CustomActionData not defined");

    // Tab-delimited arguments:
    //
    // 0: Full path to file.
    // 1: Value to write to file.
    hr = StrSplitAllocArray(&rgwzArgs, &cArgs, pwzCustomActionData, L"\t");
    ExitOnFailure(hr, "failed to split CustomActionData");

    if (cArgs != 2)
    {
        // All varargs have to be LPWSTR.
        LPCWSTR pwzExpected = L"2";
        WCHAR wzActual[11] = {};

        _itow_s(cArgs, wzActual, 10);
        MessageExitOnFailure(hr = E_INVALIDARG, msidberrCustomActionDataInvalid, "expected %ls arguments, got %ls", pwzExpected, wzActual);
    }

    LPCWSTR pwzPath = rgwzArgs[0];
    LPCWSTR pwzContent = rgwzArgs[1];

    hRecord = ::MsiCreateRecord(2);
    hr = WcaSetRecordString(hRecord, 1, pwzPath);
    ExitOnFailure(hr, "failed to set path in record");

    hr = WcaSetRecordString(hRecord, 2, pwzContent);
    ExitOnFailure(hr, "failed to set content in record");

    WcaProcessMessage(INSTALLMESSAGE_ACTIONDATA, hRecord);

    hr = FileFromString(pwzPath, FILE_ATTRIBUTE_ARCHIVE | FILE_ATTRIBUTE_HIDDEN, pwzContent, FILE_ENCODING_UTF8);
    MessageExitOnFailure(hr, msidberrFileWriteFailed, "failed to write file '%ls', content: %ls", pwzPath, pwzContent);

LExit:
    ReleaseStrArray(rgwzArgs, cArgs);
    ReleaseStr(pwzCustomActionData);

    return WcaFinalize(SUCCEEDED(hr) ? ERROR_SUCCESS : ERROR_INSTALL_FAILURE);
}
