package com.microsoft.azure.simpletodo.model;

import com.fasterxml.jackson.annotation.JsonProperty;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.EqualsAndHashCode;
import lombok.Getter;
import lombok.Setter;
import lombok.ToString;
import org.springframework.format.annotation.DateTimeFormat;

import javax.annotation.Generated;
import javax.validation.Valid;
import javax.validation.constraints.NotNull;
import java.time.OffsetDateTime;

/**
 * A task that needs to be completed
 */
@Getter
@Setter
@ToString
@EqualsAndHashCode(onlyExplicitlyIncluded = true)
@Schema(name = "TodoItem", description = "A task that needs to be completed")
@Generated(value = "org.openapitools.codegen.languages.SpringCodegen")
public class TodoItem {

    @EqualsAndHashCode.Include // items are equal if they have the same `listId` and `id`
    @JsonProperty("id")
    @Schema(name = "id", required = false)
    private String id;

    @EqualsAndHashCode.Include
    @NotNull
    @JsonProperty("listId")
    @Schema(name = "listId", required = true)
    private String listId;

    @NotNull
    @JsonProperty("name")
    @Schema(name = "name", required = true)
    private String name;

    @JsonProperty("description")
    @Schema(name = "description", required = true)
    private String description;

    @Valid
    @JsonProperty("state")
    @Schema(name = "state", required = false)
    private TodoState state;

    @Valid
    @JsonProperty("dueDate")
    @Schema(name = "dueDate", required = false)
    @DateTimeFormat(iso = DateTimeFormat.ISO.DATE_TIME)
    private OffsetDateTime dueDate;

    @Valid
    @JsonProperty("completedDate")
    @Schema(name = "completedDate", required = false)
    @DateTimeFormat(iso = DateTimeFormat.ISO.DATE_TIME)
    private OffsetDateTime completedDate;
}

