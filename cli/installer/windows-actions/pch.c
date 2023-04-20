// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

#include "pch.h"

LPVOID Alloc(_In_ size_t cbSize, _In_ BOOL fZero)
{
    return HeapAlloc(GetProcessHeap(), fZero ? HEAP_ZERO_MEMORY : 0, cbSize);
}

LPVOID ReAlloc(_In_ LPVOID pv, _In_ size_t cbSize, _In_ BOOL fZero)
{
    return HeapReAlloc(GetProcessHeap(), fZero ? HEAP_ZERO_MEMORY : 0, pv, cbSize);
}

BOOL Free(_In_ LPVOID pv)
{
    return HeapFree(GetProcessHeap(), 0, pv);
}

UINT AllocString(_In_ size_t cch, _Out_ LPWSTR* ppwz)
{
    LPWSTR pwz = (LPWSTR)Alloc(cch * sizeof(WCHAR), FALSE);
    if (!pwz)
    {
        return ERROR_OUTOFMEMORY;
    }

    *ppwz = pwz;
    return ERROR_SUCCESS;
}

BOOL FreeString(_In_ LPWSTR pwz)
{
    return Free(pwz);
}

UINT GetProperty(_In_ MSIHANDLE hSession, _In_ LPCWSTR pwzName, _Out_ LPWSTR* ppwzValue)
{
    size_t cchValue = 0;
    UINT err = MsiGetPropertyW(hSession, pwzName, NULL, &cchValue);
    if (err != ERROR_SUCCESS)
    {
        return err;
    }

    // Leave room for terminating NUL.
    cchValue++;
    
    LPWSTR pwzValue = NULL;
    err = AllocString(cchValue, &pwzValue);
    if (err != ERROR_SUCCESS)
    {
        return err;
    }

    err = MsiGetPropertyW(hSession, pwzName, pwzValue, &cchValue);
    if (err != ERROR_SUCCESS)
    {
        FreeString(pwzValue);
        return err;
    }

    *ppwzValue = pwzValue;
    return ERROR_SUCCESS;
}

UINT Log(_In_ MSIHANDLE hSession, _In_ INSTALLMESSAGE dwType, _In_ LPCWSTR pwzTemplate)
{
    MSIHANDLE hRecord = MsiCreateRecord(0);
    if (!hRecord)
    {
        return ERROR_INSTALL_FAILURE;
    }

    UINT err = MsiRecordSetStringW(hRecord, 0, pwzTemplate);
    if (err != ERROR_SUCCESS)
    {
        MsiCloseHandle(hRecord);
        return err;
    }

    err = MsiProcessMessage(hSession, dwType, hRecord);
    MsiCloseHandle(hRecord);

    return err;
}

UINT Log2(_In_ MSIHANDLE hSession, _In_ INSTALLMESSAGE dwType, _In_ LPCWSTR pwzTemplate, _In_ LPCWSTR pwz1, _In_ UINT dw2)
{
    MSIHANDLE hRecord = MsiCreateRecord(1);
    if (!hRecord)
    {
        return ERROR_INSTALL_FAILURE;
    }

    UINT err = MsiRecordSetStringW(hRecord, 0, pwzTemplate);
    if (err != ERROR_SUCCESS)
    {
        MsiCloseHandle(hRecord);
        return err;
    }

    err = MsiRecordSetStringW(hRecord, 1, pwz1);
    if (err != ERROR_SUCCESS)
    {
        MsiCloseHandle(hRecord);
        return err;
    }

    err = MsiRecordSetInteger(hRecord, 2, dw2);
    if (err != ERROR_SUCCESS)
    {
        MsiCloseHandle(hRecord);
        return err;
    }

    err = MsiProcessMessage(hSession, dwType, hRecord);
    MsiCloseHandle(hRecord);

    return err;
}
