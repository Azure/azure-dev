package com.microsoft.azure.simpletodo.model;

import com.fasterxml.jackson.annotation.JsonProperty;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.OffsetDateTime;
import java.util.Objects;
import javax.annotation.Generated;
import javax.validation.Valid;
import javax.validation.constraints.NotNull;
import org.springframework.format.annotation.DateTimeFormat;

/**
 * A task that needs to be completed
 */

@Schema(name = "TodoItem", description = "A task that needs to be completed")
@Generated(value = "org.openapitools.codegen.languages.SpringCodegen")
public class TodoItem {

    @JsonProperty("id")
    private String id;

    @JsonProperty("listId")
    private String listId;

    @JsonProperty("name")
    private String name;

    @JsonProperty("description")
    private String description;

    @JsonProperty("state")
    private TodoState state;

    @JsonProperty("dueDate")
    @DateTimeFormat(iso = DateTimeFormat.ISO.DATE_TIME)
    private OffsetDateTime dueDate;

    @JsonProperty("completedDate")
    @DateTimeFormat(iso = DateTimeFormat.ISO.DATE_TIME)
    private OffsetDateTime completedDate;

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
     * Get listId
     * @return listId
     */
    @NotNull
    @Schema(name = "listId", required = true)
    public String getListId() {
        return listId;
    }

    public void setListId(String listId) {
        this.listId = listId;
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
    @NotNull
    @Schema(name = "description", required = true)
    public String getDescription() {
        return description;
    }

    public void setDescription(String description) {
        this.description = description;
    }

    /**
     * Get state
     * @return state
     */
    @Valid
    @Schema(name = "state", required = false)
    public TodoState getState() {
        return state;
    }

    public void setState(TodoState state) {
        this.state = state;
    }

    /**
     * Get dueDate
     * @return dueDate
     */
    @Valid
    @Schema(name = "dueDate", required = false)
    public OffsetDateTime getDueDate() {
        return dueDate;
    }

    public void setDueDate(OffsetDateTime dueDate) {
        this.dueDate = dueDate;
    }

    /**
     * Get completedDate
     * @return completedDate
     */
    @Valid
    @Schema(name = "completedDate", required = false)
    public OffsetDateTime getCompletedDate() {
        return completedDate;
    }

    public void setCompletedDate(OffsetDateTime completedDate) {
        this.completedDate = completedDate;
    }

    public boolean equals(final Object o) {
        // items are equal if they have the same `listId` and `id`
        if (o == this) return true;
        if (!(o instanceof TodoItem)) return false;
        final TodoItem other = (TodoItem) o;
        if (!((Object) this instanceof TodoItem)) return false;
        final Object this$id = this.getId();
        final Object other$id = other.getId();
        if (this$id == null ? other$id != null : !this$id.equals(other$id)) return false;
        final Object this$listId = this.getListId();
        final Object other$listId = other.getListId();
        if (this$listId == null ? other$listId != null : !this$listId.equals(other$listId)) return false;
        return true;
    }

    public int hashCode() {
        return Objects.hash(this.listId, this.id);
    }

    public String toString() {
        return (
            "TodoItem(id=" +
            this.getId() +
            ", listId=" +
            this.getListId() +
            ", name=" +
            this.getName() +
            ", description=" +
            this.getDescription() +
            ", state=" +
            this.getState() +
            ", dueDate=" +
            this.getDueDate() +
            ", completedDate=" +
            this.getCompletedDate() +
            ")"
        );
    }
}
