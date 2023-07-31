/*
 * Copyright (c) Microsoft Corporation. All rights reserved.
 * Licensed under the MIT License. See License.txt in the project root for license information.
 */

package com.microsoft.azure.simpletodo.configuration;

import com.microsoft.azure.simpletodo.model.TodoState;
import org.springframework.core.convert.converter.Converter;

public class StringToTodoStateConverter implements Converter<String, TodoState> {

    @Override
    public TodoState convert(String source) {
        return TodoState.fromValue(source);
    }
}
