package com.microsoft.azure.simpletodo.web;

import com.microsoft.azure.simpletodo.model.TodoList;
import com.microsoft.azure.simpletodo.repository.TodoListRepository;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.Parameter;
import io.swagger.v3.oas.annotations.media.Content;
import io.swagger.v3.oas.annotations.media.Schema;
import io.swagger.v3.oas.annotations.responses.ApiResponse;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestMethod;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.servlet.support.ServletUriComponentsBuilder;

import javax.validation.Valid;
import javax.validation.constraints.NotNull;
import java.math.BigDecimal;
import java.net.URI;
import java.util.List;

@RequestMapping("/lists")
@RestController
@RequiredArgsConstructor
@Validated
@Tag(name = "Lists", description = "the Lists API")
public class TodoListsController {
    private final TodoListRepository todoListRepository;

    /**
     * POST /lists : Creates a new Todo list
     *
     * @param todoList The Todo List (optional)
     * @return A Todo list result (status code 201)
     * or Invalid request schema (status code 400)
     */
    @Operation(
        operationId = "createList",
        summary = "Creates a new Todo list",
        tags = {"Lists"},
        responses = {
            @ApiResponse(responseCode = "201", description = "A Todo list result", content = {
                @Content(mediaType = MediaType.APPLICATION_JSON_VALUE, schema = @Schema(implementation = TodoList.class))
            }),
            @ApiResponse(responseCode = "400", description = "Invalid request schema")
        }
    )
    @RequestMapping(
        method = RequestMethod.POST,
        produces = MediaType.APPLICATION_JSON_VALUE,
        consumes = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoList> createList(
        @Parameter(name = "TodoList", description = "The Todo List") @Valid @RequestBody(required = false) TodoList todoList
    ) {
        final TodoList savedTodoList = todoListRepository.save(todoList);
        URI location = ServletUriComponentsBuilder
            .fromCurrentRequest()
            .path("/{id}")
            .buildAndExpand(savedTodoList.getId())
            .toUri();
        return ResponseEntity.created(location).body(savedTodoList);
    }

    /**
     * DELETE /lists/{listId} : Deletes a Todo list by unique identifier
     *
     * @param listId The Todo list unique identifier (required)
     * @return Todo list deleted successfully (status code 204)
     * or Todo list not found (status code 404)
     */
    @Operation(
        operationId = "deleteListById",
        summary = "Deletes a Todo list by unique identifier",
        tags = {"Lists"},
        responses = {
            @ApiResponse(responseCode = "204", description = "Todo list deleted successfully"),
            @ApiResponse(responseCode = "404", description = "Todo list not found")
        }
    )
    @RequestMapping(
        method = RequestMethod.DELETE,
        value = "/{listId}"
    )
    public ResponseEntity<Void> deleteListById(
        @Parameter(name = "listId", description = "The Todo list unique identifier", required = true) @PathVariable("listId") String listId
    ) {
        return todoListRepository.findById(listId)
            .map(l -> todoListRepository.deleteTodoListById(l.getId()))
            .map(l -> ResponseEntity.noContent().<Void>build())
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    /**
     * GET /lists/{listId} : Gets a Todo list by unique identifier
     *
     * @param listId The Todo list unique identifier (required)
     * @return A Todo list result (status code 200)
     * or Todo list not found (status code 404)
     */
    @Operation(
        operationId = "getListById",
        summary = "Gets a Todo list by unique identifier",
        tags = {"Lists"},
        responses = {
            @ApiResponse(responseCode = "200", description = "A Todo list result", content = {
                @Content(mediaType = MediaType.APPLICATION_JSON_VALUE, schema = @Schema(implementation = TodoList.class))
            }),
            @ApiResponse(responseCode = "404", description = "Todo list not found")
        }
    )
    @RequestMapping(
        method = RequestMethod.GET,
        value = "/{listId}",
        produces = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoList> getListById(
        @Parameter(name = "listId", description = "The Todo list unique identifier", required = true) @PathVariable("listId") String listId
    ) {
        return todoListRepository.findById(listId)
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    /**
     * GET /lists : Gets an array of Todo lists
     *
     * @param top  The max number of items to returns in a result (optional)
     * @param skip The number of items to skip within the results (optional)
     * @return An array of Todo lists (status code 200)
     */
    @Operation(
        operationId = "getLists",
        summary = "Gets an array of Todo lists",
        tags = {"Lists"},
        responses = {
            @ApiResponse(responseCode = "200", description = "An array of Todo lists", content = {
                @Content(mediaType = MediaType.APPLICATION_JSON_VALUE, schema = @Schema(implementation = TodoList.class))
            })
        }
    )
    @RequestMapping(
        method = RequestMethod.GET,
        produces = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<List<TodoList>> getLists(
        @Parameter(name = "top", description = "The max number of items to returns in a result, 20 by default") @Valid @RequestParam(value = "top", required = false, defaultValue = "20") BigDecimal top,
        @Parameter(name = "skip", description = "The number of items to skip within the results, 0 by default ") @Valid @RequestParam(value = "skip", required = false, defaultValue = "0") BigDecimal skip
    ) {
        return ResponseEntity.ok(todoListRepository.findAll(skip.intValue(), top.intValue()));
    }


    /**
     * PUT /lists/{listId} : Updates a Todo list by unique identifier
     *
     * @param listId   The Todo list unique identifier (required)
     * @param todoList The Todo List (optional)
     * @return A Todo list result (status code 200)
     * or Todo list is invalid (status code 400)
     */
    @Operation(
        operationId = "updateListById",
        summary = "Updates a Todo list by unique identifier",
        tags = {"Lists"},
        responses = {
            @ApiResponse(responseCode = "200", description = "A Todo list result", content = {
                @Content(mediaType = MediaType.APPLICATION_JSON_VALUE, schema = @Schema(implementation = TodoList.class))
            }),
            @ApiResponse(responseCode = "400", description = "Todo list is invalid")
        }
    )
    @RequestMapping(
        method = RequestMethod.PUT,
        value = "/{listId}",
        produces = MediaType.APPLICATION_JSON_VALUE,
        consumes = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoList> updateListById(
        @Parameter(name = "listId", description = "The Todo list unique identifier", required = true) @PathVariable(value = "listId", required = true) String listId,
        @Parameter(name = "TodoList", description = "The Todo List") @Valid @RequestBody @NotNull TodoList todoList
    ) {
        todoList.setId(listId);
        return todoListRepository.findById(listId)
            .map(t -> ResponseEntity.ok(todoListRepository.save(todoList)))
            .orElseGet(() -> ResponseEntity.notFound().build());
    }
}
