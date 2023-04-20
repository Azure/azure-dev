// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

#pragma once

#include <windows.h>
#include <msi.h>
#include <msiquery.h>

LPVOID Alloc(_In_ size_t cbSize, _In_ BOOL fZero);
LPVOID ReAlloc(_In_ LPVOID pv, _In_ size_t cbSize, _In_ BOOL fZero);
BOOL Free(_In_ LPVOID pv);

UINT AllocString(_In_ size_t cch, _Out_ LPWSTR* ppwz);
BOOL FreeString(_In_ LPWSTR pwz);

UINT GetProperty(_In_ MSIHANDLE hSession, _In_ LPCWSTR pwzName, _Out_ LPWSTR* ppwzValue);
UINT Log(_In_ MSIHANDLE hSession, _In_ INSTALLMESSAGE dwType, _In_ LPCWSTR pwzMessage);
UINT Log2(_In_ MSIHANDLE hSession, _In_ INSTALLMESSAGE dwType, _In_ LPCWSTR pwzTemplate, _In_ LPCWSTR pwz1, _In_ UINT dw2);
