/*
 * Copyright (c) Microsoft Corporation. All rights reserved.
 * Licensed under the MIT License. See License.txt in the project root for license information.
 */

package com.microsoft.azure.simpletodo.controller;

import com.microsoft.azure.simpletodo.model.TodoItem;
import com.microsoft.azure.simpletodo.model.TodoList;
import com.microsoft.azure.simpletodo.model.TodoState;
import com.microsoft.azure.simpletodo.repository.TodoItemRepository;
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
import java.math.BigDecimal;
import java.net.URI;
import java.util.List;
import java.util.Optional;

@RequestMapping("/lists/{listId}/items")
@RestController
@RequiredArgsConstructor
@Validated
@Tag(name = "Items", description = "the Items API")
public class TodoItemsController {
    private final TodoListRepository todoListRepository;
    private final TodoItemRepository todoItemRepository;

    /**
     * POST /lists/{listId}/items : Creates a new Todo item within a list
     *
     * @param listId   The Todo list unique identifier (required)
     * @param todoItem The Todo Item (optional)
     * @return A Todo item result (status code 201)
     * or Todo list not found (status code 404)
     */
    @Operation(
        operationId = "createItem",
        summary = "Creates a new Todo item within a list",
        tags = {"Items"},
        responses = {
            @ApiResponse(responseCode = "201", description = "A Todo item result", content = {
                @Content(mediaType = MediaType.APPLICATION_JSON_VALUE, schema = @Schema(implementation = TodoItem.class))
            }),
            @ApiResponse(responseCode = "404", description = "Todo list not found")
        }
    )
    @RequestMapping(
        method = RequestMethod.POST,
        produces = MediaType.APPLICATION_JSON_VALUE,
        consumes = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoItem> createItem(
        @Parameter(name = "listId", description = "The Todo list unique identifier", required = true) @PathVariable(value = "listId")
        String listId,
        @Parameter(name = "todoItem", description = "The Todo item") @Valid @RequestBody(required = false)
        TodoItem todoItem
    ) {
        final Optional<TodoList> optionalTodoList = todoListRepository.findById(listId);
        if (optionalTodoList.isPresent()) {
            todoItem.setListId(listId);
            final TodoItem savedTodoItem = todoItemRepository.save(todoItem);
            final URI location = ServletUriComponentsBuilder
                .fromCurrentRequest()
                .path("/{id}")
                .buildAndExpand(savedTodoItem.getId())
                .toUri();
            return ResponseEntity.created(location).body(savedTodoItem);
        } else {
            return ResponseEntity.notFound().build();
        }
    }

    /**
     * DELETE /lists/{listId}/items/{itemId} : Deletes a Todo item by unique identifier
     *
     * @param listId The Todo list unique identifier (required)
     * @param itemId The Todo item unique identifier (required)
     * @return Todo item deleted successfully (status code 204)
     * or Todo list or item not found (status code 404)
     */
    @Operation(
        operationId = "deleteItemById",
        summary = "Deletes a Todo item by unique identifier",
        tags = {"Items"},
        responses = {
            @ApiResponse(responseCode = "204", description = "Todo item deleted successfully"),
            @ApiResponse(responseCode = "404", description = "Todo list or item not found")
        }
    )
    @RequestMapping(
        method = RequestMethod.DELETE,
        value = "/{itemId}"
    )
    public ResponseEntity<Void> deleteItemById(
        @Parameter(name = "listId", description = "The Todo list unique identifier", required = true) @PathVariable("listId") String listId,
        @Parameter(name = "itemId", description = "The Todo item unique identifier", required = true) @PathVariable("itemId") String itemId
    ) {
        return todoItemRepository.findTodoItemByListIdAndId(listId, itemId)
            .map(i -> todoItemRepository.deleteTodoItemByListIdAndId(i.getListId(), i.getId()))
            .map(i -> ResponseEntity.noContent().<Void>build())
            .orElse(ResponseEntity.notFound().build());
    }

    /**
     * GET /lists/{listId}/items/{itemId} : Gets a Todo item by unique identifier
     *
     * @param listId The Todo list unique identifier (required)
     * @param itemId The Todo item unique identifier (required)
     * @return A Todo item result (status code 200)
     * or Todo list or item not found (status code 404)
     */
    @Operation(
        operationId = "getItemById",
        summary = "Gets a Todo item by unique identifier",
        tags = {"Items"},
        responses = {
            @ApiResponse(responseCode = "200", description = "A Todo item result", content = {
                @Content(mediaType = MediaType.APPLICATION_JSON_VALUE, schema = @Schema(implementation = TodoItem.class))
            }),
            @ApiResponse(responseCode = "404", description = "Todo list or item not found")
        }
    )
    @RequestMapping(
        method = RequestMethod.GET,
        value = "/{itemId}",
        produces = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoItem> getItemById(
        @Parameter(name = "listId", description = "The Todo list unique identifier", required = true) @PathVariable("listId") String listId,
        @Parameter(name = "itemId", description = "The Todo item unique identifier", required = true) @PathVariable("itemId") String itemId
    ) {
        return todoItemRepository.findTodoItemByListIdAndId(listId, itemId)
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    /**
     * GET /lists/{listId}/items : Gets Todo items within the specified list
     *
     * @param listId The Todo list unique identifier (required)
     * @param top    The max number of items to returns in a result (optional)
     * @param skip   The number of items to skip within the results (optional)
     * @return An array of Todo items (status code 200)
     * or Todo list not found (status code 404)
     */
    @Operation(
        operationId = "getItemsByListId",
        summary = "Gets Todo items within the specified list",
        tags = {"Items"},
        responses = {
            @ApiResponse(responseCode = "200", description = "An array of Todo items", content = {
                @Content(mediaType = MediaType.APPLICATION_JSON_VALUE, schema = @Schema(implementation = TodoItem.class))
            }),
            @ApiResponse(responseCode = "404", description = "Todo list not found")
        }
    )
    @RequestMapping(
        method = RequestMethod.GET,
        produces = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<List<TodoItem>> getItemsByListId(
        @Parameter(name = "listId", description = "The Todo list unique identifier", required = true) @PathVariable("listId") String listId,
        @Parameter(name = "top", description = "The max number of items to returns in a result") @Valid @RequestParam(value = "top", required = false, defaultValue = "20") BigDecimal top,
        @Parameter(name = "skip", description = "The number of items to skip within the results") @Valid @RequestParam(value = "skip", required = false, defaultValue = "0") BigDecimal skip
    ) {
        return todoListRepository.findById(listId)
            .map(l -> todoItemRepository.findTodoItemsByTodoList(l.getId(), skip.intValue(), top.intValue()))
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    /**
     * PUT /lists/{listId}/items/{itemId} : Updates a Todo item by unique identifier
     *
     * @param listId   The Todo list unique identifier (required)
     * @param itemId   The Todo item unique identifier (required)
     * @param todoItem The Todo Item (optional)
     * @return A Todo item result (status code 200)
     * or Todo item is invalid (status code 400)
     * or Todo list or item not found (status code 404)
     */
    @Operation(
        operationId = "updateItemById",
        summary = "Updates a Todo item by unique identifier",
        tags = {"Items"},
        responses = {
            @ApiResponse(responseCode = "200", description = "A Todo item result", content = {
                @Content(mediaType = MediaType.APPLICATION_JSON_VALUE, schema = @Schema(implementation = TodoItem.class))
            }),
            @ApiResponse(responseCode = "400", description = "Todo item is invalid"),
            @ApiResponse(responseCode = "404", description = "Todo list or item not found")
        }
    )
    @RequestMapping(
        method = RequestMethod.PUT,
        value = "/{itemId}",
        produces = MediaType.APPLICATION_JSON_VALUE,
        consumes = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoItem> updateItemById(
        @Parameter(name = "listId", description = "The Todo list unique identifier", required = true) @PathVariable("listId") String listId,
        @Parameter(name = "itemId", description = "The Todo item unique identifier", required = true) @PathVariable("itemId") String itemId,
        @Parameter(name = "TodoItem", description = "The Todo Item") @Valid @RequestBody(required = false) TodoItem todoItem
    ) {
        todoItem.setId(itemId);
        todoItem.setListId(listId);
        return todoItemRepository.findTodoItemByListIdAndId(listId, itemId)
            .map(t -> todoItemRepository.save(todoItem))
            .map(ResponseEntity::ok) // return the saved item.
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    /**
     * GET /lists/{listId}/items/state/{state} : Gets a list of Todo items of a specific state
     *
     * @param listId The Todo list unique identifier (required)
     * @param state  The Todo item state (required)
     * @param top    The max number of items to returns in a result (optional)
     * @param skip   The number of items to skip within the results (optional)
     * @return An array of Todo items (status code 200)
     * or Todo list or item not found (status code 404)
     */
    @Operation(
        operationId = "getItemsByListIdAndState",
        summary = "Gets a list of Todo items of a specific state",
        tags = {"Items"},
        responses = {
            @ApiResponse(responseCode = "200", description = "An array of Todo items", content = {
                @Content(mediaType = MediaType.APPLICATION_JSON_VALUE, schema = @Schema(implementation = TodoItem.class))
            }),
            @ApiResponse(responseCode = "404", description = "Todo list or item not found")
        }
    )
    @RequestMapping(
        method = RequestMethod.GET,
        value = "/state/{state}",
        produces = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<List<TodoItem>> getItemsByListIdAndState(
        @Parameter(name = "listId", description = "The Todo list unique identifier", required = true) @PathVariable("listId") String listId,
        @Parameter(name = "state", description = "The Todo item state", required = true) @PathVariable("state") TodoState state,
        @Parameter(name = "top", description = "The max number of items to returns in a result") @Valid @RequestParam(value = "top", required = false, defaultValue = "20") BigDecimal top,
        @Parameter(name = "skip", description = "The number of items to skip within the results") @Valid @RequestParam(value = "skip", required = false, defaultValue = "0") BigDecimal skip
    ) {
        return todoListRepository.findById(listId)
            .map(l -> todoItemRepository.findTodoItemsByTodoListAndState(l.getId(), state.name(), skip.intValue(), top.intValue()))
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    /**
     * PUT /lists/{listId}/items/state/{state} : Changes the state of the specified list items
     *
     * @param listId The Todo list unique identifier (required)
     * @param state  The Todo item state (required)
     * @return Todo items updated (status code 204)
     * or Update request is invalid (status code 400)
     */
    @Operation(
        operationId = "updateItemsStateByListId",
        summary = "Changes the state of the specified list items",
        tags = {"Items"},
        responses = {
            @ApiResponse(responseCode = "204", description = "Todo items updated"),
            @ApiResponse(responseCode = "400", description = "Update request is invalid")
        }
    )
    @RequestMapping(
        method = RequestMethod.PUT,
        value = "/state/{state}",
        consumes = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<Void> updateItemsStateByListId(
        @Parameter(name = "listId", description = "The Todo list unique identifier", required = true) @PathVariable("listId") String listId,
        @Parameter(name = "state", description = "The Todo item state", required = true) @PathVariable("state") TodoState state
    ) {
        final List<TodoItem> items = todoItemRepository.findTodoItemsByListId(listId);
        items.forEach(item -> item.setState(state));
        todoItemRepository.saveAll(items); // save items in batch.
        return ResponseEntity.noContent().build();
    }
}
