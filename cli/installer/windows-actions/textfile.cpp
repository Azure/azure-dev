// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

#include "pch.h"
#include <fileutil.h>
#include <strutil.h>

extern "C" UINT WINAPI WriteTextFile(__in MSIHANDLE hSession)
{
    HRESULT hr = S_OK;
    LPWSTR pwzCustomActionData = NULL;
    LPWSTR* rgwzArgs = NULL;
    UINT cArgs = 0;

    hr = WcaInitialize(hSession, __FUNCTION__);
    ExitOnFailure(hr, "failed to initialize");

    hr = WcaGetProperty(L"CustomActionData", &pwzCustomActionData);
    ExitOnFailure(hr, "CustomActionData not defined");

    // Tab-delimited arguments:
    //
    // 0: Full path to file.
    // 1: Value to write to file.

    hr = StrSplitAllocArray(&rgwzArgs, &cArgs, pwzCustomActionData, L"\t");
    ExitOnFailure(hr, "failed to split CustomActionData");

    if (cArgs != 2)
    {
        ExitOnFailure(hr = E_INVALIDARG, "expected 2 arguments, got %d", cArgs);
    }

    LPCWSTR pwzPath = rgwzArgs[0];
    LPCWSTR pwzValue = rgwzArgs[1];

    hr = FileFromString(pwzPath, FILE_ATTRIBUTE_ARCHIVE | FILE_ATTRIBUTE_HIDDEN, pwzValue, FILE_ENCODING_UTF8);
    ExitOnFailure(hr, "failed to write '%ls' to file path %ls", pwzValue, pwzPath);

LExit:
    ReleaseStrArray(rgwzArgs, cArgs);
    ReleaseStr(pwzCustomActionData);

    return WcaFinalize(SUCCEEDED(hr) ? ERROR_SUCCESS : ERROR_INSTALL_FAILURE);
}
