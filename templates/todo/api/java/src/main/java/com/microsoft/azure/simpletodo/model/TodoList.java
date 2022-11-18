package com.microsoft.azure.simpletodo.model;

import com.fasterxml.jackson.annotation.JsonProperty;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.Objects;
import javax.annotation.Generated;
import javax.validation.constraints.NotNull;

/**
 * A list of related Todo items
 */

@Schema(name = "TodoList", description = " A list of related Todo items")
@Generated(value = "org.openapitools.codegen.languages.SpringCodegen")
public class TodoList {

    @JsonProperty("id")
    private String id;

    @JsonProperty("name")
    private String name;

    @JsonProperty("description")
    private String description;

    /**
     * Get id
     * @return id
     */

    @Schema(name = "id", required = false)
    public String getId() {
        return id;
    }

    public void setId(String id) {
        this.id = id;
    }

    /**
     * Get name
     * @return name
     */
    @NotNull
    @Schema(name = "name", required = true)
    public String getName() {
        return name;
    }

    public void setName(String name) {
        this.name = name;
    }

    /**
     * Get description
     * @return description
     */

    @Schema(name = "description", required = false)
    public String getDescription() {
        return description;
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
