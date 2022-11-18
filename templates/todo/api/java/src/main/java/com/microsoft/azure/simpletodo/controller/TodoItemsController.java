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
import java.math.BigDecimal;
import java.net.URI;
import java.util.List;
import java.util.Optional;
import java.util.stream.StreamSupport;
import org.springframework.http.ResponseEntity;
import org.springframework.util.CollectionUtils;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.servlet.support.ServletUriComponentsBuilder;

@RestController
public class TodoItemsController implements ItemsApi {

    private final TodoListRepository todoListRepository;

    private final TodoItemRepository todoItemRepository;

    public TodoItemsController(TodoListRepository todoListRepository, TodoItemRepository todoItemRepository) {
        this.todoListRepository = todoListRepository;
        this.todoItemRepository = todoItemRepository;
    }

    public ResponseEntity<TodoItem> createItem(String listId, TodoItem todoItem) {
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

    public ResponseEntity<Void> deleteItemById(String listId, String itemId) {
        return todoItemRepository
            .findTodoItemByListIdAndId(listId, itemId)
            .map(i -> todoItemRepository.deleteTodoItemByListIdAndId(i.getListId(), i.getId()))
            .map(i -> ResponseEntity.noContent().<Void>build())
            .orElse(ResponseEntity.notFound().build());
    }

    public ResponseEntity<TodoItem> getItemById(String listId, String itemId) {
        return todoItemRepository
            .findTodoItemByListIdAndId(listId, itemId)
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    public ResponseEntity<List<TodoItem>> getItemsByListId(String listId, BigDecimal top, BigDecimal skip) {
        // no need to check nullity of top and skip, because they have default values.
        return todoListRepository
            .findById(listId)
            .map(l -> todoItemRepository.findTodoItemsByTodoList(l.getId(), skip.intValue(), top.intValue()))
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    public ResponseEntity<TodoItem> updateItemById(String listId, String itemId, TodoItem todoItem) {
        // make sure listId and itemId are set into the todoItem, otherwise it will create
        // a new todo item.
        todoItem.setId(itemId);
        todoItem.setListId(listId);
        return todoItemRepository
            .findTodoItemByListIdAndId(listId, itemId)
            .map(t -> todoItemRepository.save(todoItem))
            .map(ResponseEntity::ok) // return the saved item.
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    public ResponseEntity<List<TodoItem>> getItemsByListIdAndState(
        String listId,
        TodoState state,
        BigDecimal top,
        BigDecimal skip
    ) {
        // no need to check nullity of top and skip, because they have default values.
        return todoListRepository
            .findById(listId)
            .map(l ->
                todoItemRepository.findTodoItemsByTodoListAndState(l.getId(), state.name(), skip.intValue(), top.intValue())
            )
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    public ResponseEntity<Void> updateItemsStateByListId(String listId, TodoState state, List<String> itemIds) {
        // update all items in list with the given state if `itemIds` is not specified.
        final List<TodoItem> items = Optional
            .ofNullable(itemIds)
            .filter(ids -> !CollectionUtils.isEmpty(ids))
            .map(ids ->
                StreamSupport
                    .stream(todoItemRepository.findAllById(ids).spliterator(), false)
                    .filter(i -> listId.equalsIgnoreCase(i.getListId()))
                    .toList()
            )
            .orElseGet(() -> todoItemRepository.findTodoItemsByListId(listId));
        items.forEach(item -> item.setState(state));
        todoItemRepository.saveAll(items); // save items in batch.
        return ResponseEntity.noContent().build();
    }
}
