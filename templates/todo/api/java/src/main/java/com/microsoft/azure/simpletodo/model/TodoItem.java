package com.microsoft.azure.simpletodo.model;

import com.fasterxml.jackson.annotation.JsonProperty;
import io.swagger.v3.oas.annotations.media.Schema;
import org.springframework.format.annotation.DateTimeFormat;

import javax.annotation.Generated;
import javax.validation.Valid;
import javax.validation.constraints.NotNull;
import java.time.OffsetDateTime;
import java.util.Objects;

/**
 * A task that needs to be completed
 */

@Schema(name = "TodoItem", description = "A task that needs to be completed")
@Generated(value = "org.openapitools.codegen.languages.SpringCodegen", date = "2022-03-15T23:13:58.701016+01:00[Europe/Berlin]")
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

    public TodoItem id(String id) {
        this.id = id;
        return this;
    }

    /**
     * Get id
     *
     * @return id
     */

    @Schema(name = "id", required = false)
    public String getId() {
        return id;
    }

    public void setId(String id) {
        this.id = id;
    }

    public TodoItem listId(String listId) {
        this.listId = listId;
        return this;
    }

    /**
     * Get listId
     *
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

    public TodoItem name(String name) {
        this.name = name;
        return this;
    }

    /**
     * Get name
     *
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

    public TodoItem description(String description) {
        this.description = description;
        return this;
    }

    /**
     * Get description
     *
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

    public TodoItem state(TodoState state) {
        this.state = state;
        return this;
    }

    /**
     * Get state
     *
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

    public TodoItem dueDate(OffsetDateTime dueDate) {
        this.dueDate = dueDate;
        return this;
    }

    /**
     * Get dueDate
     *
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

    public TodoItem completedDate(OffsetDateTime completedDate) {
        this.completedDate = completedDate;
        return this;
    }

    /**
     * Get completedDate
     *
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

    @Override
    public boolean equals(Object o) {
        if (this == o) {
            return true;
        }
        if (o == null || getClass() != o.getClass()) {
            return false;
        }
        TodoItem todoItem = (TodoItem) o;
        return Objects.equals(this.id, todoItem.id) &&
                Objects.equals(this.listId, todoItem.listId) &&
                Objects.equals(this.name, todoItem.name) &&
                Objects.equals(this.description, todoItem.description) &&
                Objects.equals(this.state, todoItem.state) &&
                Objects.equals(this.dueDate, todoItem.dueDate) &&
                Objects.equals(this.completedDate, todoItem.completedDate);
    }

    @Override
    public int hashCode() {
        return Objects.hash(id, listId, name, description, state, dueDate, completedDate);
    }

    @Override
    public String toString() {
        StringBuilder sb = new StringBuilder();
        sb.append("class TodoItem {\n");
        sb.append("    id: ").append(toIndentedString(id)).append("\n");
        sb.append("    listId: ").append(toIndentedString(listId)).append("\n");
        sb.append("    name: ").append(toIndentedString(name)).append("\n");
        sb.append("    description: ").append(toIndentedString(description)).append("\n");
        sb.append("    state: ").append(toIndentedString(state)).append("\n");
        sb.append("    dueDate: ").append(toIndentedString(dueDate)).append("\n");
        sb.append("    completedDate: ").append(toIndentedString(completedDate)).append("\n");
        sb.append("}");
        return sb.toString();
    }

    /**
     * Convert the given object to string with each line indented by 4 spaces
     * (except the first line).
     */
    private String toIndentedString(Object o) {
        if (o == null) {
            return "null";
        }
        return o.toString().replace("\n", "\n    ");
    }
}

