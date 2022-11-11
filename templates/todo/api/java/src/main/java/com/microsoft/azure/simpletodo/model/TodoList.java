package com.microsoft.azure.simpletodo.model;

import com.fasterxml.jackson.annotation.JsonProperty;
import io.swagger.v3.oas.annotations.media.Schema;

import javax.annotation.Generated;
import javax.validation.constraints.NotNull;
import java.util.Objects;

/**
 * A list of related Todo items
 */

@Schema(name = "TodoList", description = " A list of related Todo items")
@Generated(value = "org.openapitools.codegen.languages.SpringCodegen")
public class TodoList {

    @JsonProperty("id")
    private String id;

    @NotNull
    @JsonProperty("name")
    private String name;

    @JsonProperty("description")
    private String description;

    @Schema(name = "id", required = false)
    public String getId() {
        return this.id;
    }

    @Schema(name = "name", required = true)
    public @NotNull String getName() {
        return this.name;
    }

    @Schema(name = "description", required = false)
    public String getDescription() {
        return this.description;
    }

    public void setId(String id) {
        this.id = id;
    }

    public void setName(@NotNull String name) {
        this.name = name;
    }

    public void setDescription(String description) {
        this.description = description;
    }

    public boolean equals(final Object o) {
        if (o == this) return true;
        if (!(o instanceof TodoList)) return false;
        final TodoList other = (TodoList) o;
        if (!((Object) this instanceof TodoList)) return false;
        final Object this$id = this.getId();
        final Object other$id = other.getId();
        // lists are equal if they have the same id
        if (this$id == null ? other$id != null : !this$id.equals(other$id)) return false;
        return true;
    }

    public int hashCode() {
        return Objects.hash(this.getId());
    }

    public String toString() {
        return "TodoList(id=" + this.getId() + ", name=" + this.getName() + ", description=" + this.getDescription() + ")";
    }
}

