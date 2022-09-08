package com.microsoft.azure.simpletodo.model;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonValue;

import javax.annotation.Generated;

/**
 * Gets or Sets TodoState
 */

@Generated(value = "org.openapitools.codegen.languages.SpringCodegen", date = "2022-03-15T23:13:58.701016+01:00[Europe/Berlin]")
public enum TodoState {

    TODO("todo"),

    INPROGRESS("inprogress"),

    DONE("done");

    private String value;

    TodoState(String value) {
        this.value = value;
    }

    @JsonCreator
    public static TodoState fromValue(String value) {
        for (TodoState b : TodoState.values()) {
            if (b.value.equals(value)) {
                return b;
            }
        }
        throw new IllegalArgumentException("Unexpected value '" + value + "'");
    }

    @JsonValue
    public String getValue() {
        return value;
    }

    @Override
    public String toString() {
        return String.valueOf(value);
    }
}

