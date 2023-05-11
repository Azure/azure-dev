// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

#include "pch.h"

extern "C" BOOL WINAPI DllMain(__in HINSTANCE hInstance, __in DWORD dwReason, __in LPVOID)
{
    switch (dwReason)
    {
    case DLL_PROCESS_ATTACH:
        WcaGlobalInitialize(hInstance);
        break;

    case DLL_PROCESS_DETACH:
        WcaGlobalFinalize();
        break;
    }

    return TRUE;
}
