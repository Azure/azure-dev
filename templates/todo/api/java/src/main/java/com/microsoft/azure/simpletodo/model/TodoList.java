package com.microsoft.azure.simpletodo.model;

import com.fasterxml.jackson.annotation.JsonProperty;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.EqualsAndHashCode;
import lombok.Getter;
import lombok.Setter;
import lombok.ToString;

import javax.annotation.Generated;
import javax.validation.constraints.NotNull;

/**
 * A list of related Todo items
 */

@Getter
@Setter
@ToString // generate `toString()`
@EqualsAndHashCode(onlyExplicitlyIncluded = true)
@Schema(name = "TodoList", description = " A list of related Todo items")
@Generated(value = "org.openapitools.codegen.languages.SpringCodegen")
public class TodoList {

    @EqualsAndHashCode.Include // lists are equal if they have the same id
    @Schema(name = "id", required = false)
    @JsonProperty("id")
    private String id;

    @NotNull
    @Schema(name = "name", required = true)
    @JsonProperty("name")
    private String name;

    @Schema(name = "description", required = false)
    @JsonProperty("description")
    private String description;
}

