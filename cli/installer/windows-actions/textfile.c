// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

#include "pch.h"

UINT ConvertToUTF8(_In_ LPCWSTR pwzValue, _Deref_post_z_bytecap_(*pcbValue) LPSTR* ppszValue, _Out_ DWORD *pcbValue);

UINT WINAPI WriteTextFile(_In_ MSIHANDLE hSession)
{
    LPWSTR pwzCustomActionData = NULL;
    UINT err = GetProperty(hSession, L"CustomActionData", &pwzCustomActionData);
    if (err != ERROR_SUCCESS)
    {
        return err;
    }

    // Arguments must be \t-delimited:
    // 0: Full path
    // 1: Value to write
    LPWSTR pwzPath = NULL;
    LPWSTR pwzValue = NULL;
    LPWSTR pwzContext = NULL;
    LPCWSTR pwzDelims = L"\t";
    LPCWSTR pwzToken = wcstok_s(pwzCustomActionData, pwzDelims, &pwzContext);
    for (UINT i = 0; pwzToken; pwzToken = wcstok_s(NULL, pwzDelims, &pwzContext))
    {
        if (i == 0)
        {
            pwzPath = (LPWSTR)pwzToken;
        }
        else if (i == 1)
        {
            pwzValue = (LPWSTR)pwzToken;
        }
        else
        {
            Log(hSession, INSTALLMESSAGE_ERROR, L"Too many CustomActionData tokens; expected 2");
            FreeString(pwzCustomActionData);

            return ERROR_INSTALL_FAILURE;
        }

        i++;
    }

    if (!pwzPath && !pwzValue)
    {
        Log(hSession, INSTALLMESSAGE_ERROR, L"Too few CustomActionData tokens; expected 2");
        FreeString(pwzCustomActionData);

        return ERROR_INSTALL_FAILURE;
    }

    LPSTR pszUtf8Value = NULL;
    DWORD cbValue = 0;
    err = ConvertToUTF8(pwzValue, &pszUtf8Value, &cbValue);
    if (err != ERROR_SUCCESS)
    {
        Log2(hSession, INSTALLMESSAGE_ERROR, L"Failed to convert [1] to UTF8: [2]", pwzValue, err);
        FreeString(pwzCustomActionData);

        return ERROR_INSTALL_FAILURE;
    }

    // This CA currently services a specific purpose which is to create a hidden file, so hardcode it here.
    HANDLE hFile = CreateFileW(pwzPath, GENERIC_WRITE, FILE_SHARE_READ, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_HIDDEN, NULL);
    if (hFile == INVALID_HANDLE_VALUE)
    {
        err = GetLastError();
        if (err != ERROR_ALREADY_EXISTS)
        {
            Log2(hSession, INSTALLMESSAGE_ERROR, L"Failed to create text file [1]: [2]", pwzPath, err);
            Free(pszUtf8Value);
            FreeString(pwzCustomActionData);

            return ERROR_INSTALL_FAILURE;
        }
    }

    DWORD cbWritten = 0;
    if (!WriteFile(hFile, pszUtf8Value, cbValue, &cbWritten, NULL))
    {
        Log2(hSession, INSTALLMESSAGE_ERROR, L"Failed to write text file [1]: [2]", pwzPath, GetLastError());
        err = ERROR_INSTALL_FAILURE;
    }

    CloseHandle(hFile);
    Free(pszUtf8Value);
    FreeString(pwzCustomActionData);

    return err;
}

UINT ConvertToUTF8(_In_ LPCWSTR pwzValue, _Deref_post_z_bytecap_(*pcbValue) LPSTR* ppszValue, _Out_ DWORD *pcbValue)
{
    int cb = WideCharToMultiByte(CP_UTF7, 0, pwzValue, -1, NULL, 0, NULL, NULL);
    if (!cb)
    {
        return GetLastError();
    }

    // Return include space for terminating NUL.
    LPSTR pszValue = (LPSTR)Alloc(cb, FALSE);
    if (!pszValue)
    {
        return ERROR_OUTOFMEMORY;
    }

    cb = WideCharToMultiByte(CP_UTF7, 0, pwzValue, -1, pszValue, cb, NULL, NULL);
    if (!cb)
    {
        return GetLastError();
    }

    *ppszValue = pszValue;
    *pcbValue = cb;

    return ERROR_SUCCESS;
}
