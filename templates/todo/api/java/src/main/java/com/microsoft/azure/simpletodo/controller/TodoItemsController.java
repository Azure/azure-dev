/*
 * Copyright (c) Microsoft Corporation. All rights reserved.
 * Licensed under the MIT License. See License.txt in the project root for license information.
 */

package com.microsoft.azure.simpletodo.controller;

import com.microsoft.azure.simpletodo.api.ItemsApi;
import com.microsoft.azure.simpletodo.model.TodoItem;
import com.microsoft.azure.simpletodo.model.TodoList;
import com.microsoft.azure.simpletodo.model.TodoState;
import com.microsoft.azure.simpletodo.repository.TodoItemRepository;
import com.microsoft.azure.simpletodo.repository.TodoListRepository;
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
public class TodoItemsController implements ItemsApi {
    private final TodoListRepository todoListRepository;
    private final TodoItemRepository todoItemRepository;

    @RequestMapping(
        method = RequestMethod.POST,
        produces = MediaType.APPLICATION_JSON_VALUE,
        consumes = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoItem> createItem(
        @PathVariable(value = "listId") String listId,
        @Valid @RequestBody(required = false) TodoItem todoItem
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

    @RequestMapping(
        method = RequestMethod.DELETE,
        value = "/{itemId}"
    )
    public ResponseEntity<Void> deleteItemById(
        @PathVariable("listId") String listId,
        @PathVariable("itemId") String itemId
    ) {
        return todoItemRepository.findTodoItemByListIdAndId(listId, itemId)
            .map(i -> todoItemRepository.deleteTodoItemByListIdAndId(i.getListId(), i.getId()))
            .map(i -> ResponseEntity.noContent().<Void>build())
            .orElse(ResponseEntity.notFound().build());
    }

    @RequestMapping(
        method = RequestMethod.GET,
        value = "/{itemId}",
        produces = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoItem> getItemById(
        @PathVariable("listId") String listId,
        @PathVariable("itemId") String itemId
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
    @RequestMapping(
        method = RequestMethod.GET,
        produces = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<List<TodoItem>> getItemsByListId(
        @PathVariable("listId") String listId,
        @Valid @RequestParam(value = "top", required = false, defaultValue = "20") BigDecimal top,
        @Valid @RequestParam(value = "skip", required = false, defaultValue = "0") BigDecimal skip
    ) {
        return todoListRepository.findById(listId)
            .map(l -> todoItemRepository.findTodoItemsByTodoList(l.getId(), skip.intValue(), top.intValue()))
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @RequestMapping(
        method = RequestMethod.PUT,
        value = "/{itemId}",
        produces = MediaType.APPLICATION_JSON_VALUE,
        consumes = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoItem> updateItemById(
        @PathVariable("listId") String listId,
        @PathVariable("itemId") String itemId,
        @Valid @RequestBody(required = false) TodoItem todoItem
    ) {
        todoItem.setId(itemId);
        todoItem.setListId(listId);
        return todoItemRepository.findTodoItemByListIdAndId(listId, itemId)
            .map(t -> todoItemRepository.save(todoItem))
            .map(ResponseEntity::ok) // return the saved item.
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @RequestMapping(
        method = RequestMethod.GET,
        value = "/state/{state}",
        produces = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<List<TodoItem>> getItemsByListIdAndState(
        @PathVariable("listId") String listId,
        @PathVariable("state") TodoState state,
        @Valid @RequestParam(value = "top", required = false, defaultValue = "20") BigDecimal top,
        @Valid @RequestParam(value = "skip", required = false, defaultValue = "0") BigDecimal skip
    ) {
        return todoListRepository.findById(listId)
            .map(l -> todoItemRepository.findTodoItemsByTodoListAndState(l.getId(), state.name(), skip.intValue(), top.intValue()))
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @RequestMapping(
        method = RequestMethod.PUT,
        value = "/state/{state}",
        consumes = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<Void> updateItemsStateByListId(
        @PathVariable("listId") String listId,
        @PathVariable("state") TodoState state
    ) {
        final List<TodoItem> items = todoItemRepository.findTodoItemsByListId(listId);
        items.forEach(item -> item.setState(state));
        todoItemRepository.saveAll(items); // save items in batch.
        return ResponseEntity.noContent().build();
    }
}
