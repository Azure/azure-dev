package com.microsoft.azure.simpletodo.web;

import com.microsoft.azure.simpletodo.api.ListsApi;
import com.microsoft.azure.simpletodo.model.TodoItem;
import com.microsoft.azure.simpletodo.model.TodoList;
import com.microsoft.azure.simpletodo.model.TodoState;
import com.microsoft.azure.simpletodo.repository.TodoItemRepository;
import com.microsoft.azure.simpletodo.repository.TodoListRepository;
import org.springframework.data.domain.PageRequest;
import org.springframework.data.domain.Pageable;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.servlet.support.ServletUriComponentsBuilder;

import java.math.BigDecimal;
import java.net.URI;
import java.util.List;
import java.util.Optional;

@RestController
public class TodoListsController implements ListsApi {

    private final TodoListRepository todoListRepository;

    private final TodoItemRepository todoItemRepository;

    public TodoListsController(TodoListRepository todoListRepository, TodoItemRepository todoItemRepository) {
        this.todoListRepository = todoListRepository;
        this.todoItemRepository = todoItemRepository;
    }

    @Override
    public ResponseEntity<TodoItem> createItem(String listId, TodoItem todoItem) {
        Optional<TodoList> optionalTodoList = todoListRepository.findById(listId);
        if (optionalTodoList.isPresent()) {
            todoItem.setListId(listId);
            TodoItem savedTodoItem = todoItemRepository.save(todoItem);
            URI location = ServletUriComponentsBuilder
                    .fromCurrentRequest()
                    .path("/{id}")
                    .buildAndExpand(savedTodoItem.getId())
                    .toUri();
            return ResponseEntity.created(location).build();
        } else {
            return ResponseEntity.notFound().build();
        }
    }

    @Override
    public ResponseEntity<TodoList> createList(TodoList todoList) {
        try {
            TodoList savedTodoList = todoListRepository.save(todoList);
            URI location = ServletUriComponentsBuilder
                    .fromCurrentRequest()
                    .path("/{id}")
                    .buildAndExpand(savedTodoList.getId())
                    .toUri();
            return ResponseEntity.created(location).build();
        } catch (Exception e) {
            return ResponseEntity.badRequest().build();
        }
    }

    @Override
    public ResponseEntity<Void> deleteItemById(String listId, String itemId) {
        Optional<TodoItem> todoItem = getTodoItem(listId, itemId);
        if (todoItem.isPresent()) {
            todoItemRepository.deleteById(itemId);
            return ResponseEntity.status(HttpStatus.NO_CONTENT).build();
        } else {
            return ResponseEntity.notFound().build();
        }
    }

    @Override
    public ResponseEntity<Void> deleteListById(String listId) {
        Optional<TodoList> todoList = todoListRepository.findById(listId);
        if (todoList.isPresent()) {
            todoListRepository.deleteById(listId);
            return ResponseEntity.status(HttpStatus.NO_CONTENT).build();
        } else {
            return ResponseEntity.notFound().build();
        }
    }

    @Override
    public ResponseEntity<TodoItem> getItemById(String listId, String itemId) {
        return getTodoItem(listId, itemId).map(ResponseEntity::ok).orElseGet(() -> ResponseEntity.notFound().build());
    }

    @Override
    public ResponseEntity<List<TodoItem>> getItemsByListId(String listId, BigDecimal top, BigDecimal skip) {
        if (top == null) {
            top = new BigDecimal(20);
        }
        if (skip == null) {
            skip = new BigDecimal(0);
        }
        Optional<TodoList> todoList = todoListRepository.findById(listId);
        if (todoList.isPresent()) {
            return ResponseEntity.ok(todoItemRepository.findTodoItemsByTodoList(listId, PageRequest.of(skip.multiply(top).intValue(), top.intValue())));
        } else {
            return ResponseEntity.notFound().build();
        }
    }

    @Override
    public ResponseEntity<List<TodoItem>> getItemsByListIdAndState(String listId, TodoState state, BigDecimal top, BigDecimal skip) {
        if (top == null) {
            top = new BigDecimal(20);
        }
        if (skip == null) {
            skip = new BigDecimal(0);
        }
        return ResponseEntity.ok(
                todoItemRepository
                        .findTodoItemsByTodoListAndState(listId, state.name(), PageRequest.of(skip.multiply(top).intValue(), top.intValue())));
    }

    @Override
    public ResponseEntity<TodoList> getListById(String listId) {
        return todoListRepository.findById(listId).map(ResponseEntity::ok).orElseGet(() -> ResponseEntity.notFound().build());
    }

    @Override
    public ResponseEntity<List<TodoList>> getLists(BigDecimal top, BigDecimal skip) {
        if (top == null) {
            top = new BigDecimal(20);
        }
        if (skip == null) {
            skip = new BigDecimal(0);
        }
        return ResponseEntity.ok(todoListRepository.findAll(PageRequest.of(skip.multiply(top).intValue(), top.intValue())).toList());
    }

    @Override
    public ResponseEntity<TodoItem> updateItemById(String listId, String itemId, TodoItem todoItem) {
        return getTodoItem(listId, itemId).map(t -> {
            todoItemRepository.save(todoItem);
            return ResponseEntity.ok(todoItem);
        }).orElseGet(() -> ResponseEntity.notFound().build());
    }

    @Override
    public ResponseEntity<Void> updateItemsStateByListId(String listId, TodoState state, List<String> requestBody) {
        for (TodoItem todoItem : todoItemRepository.findTodoItemsByTodoList(listId, Pageable.unpaged())) {
            todoItem.state(state);
            todoItemRepository.save(todoItem);
        }
        return ResponseEntity.status(HttpStatus.NO_CONTENT).build();
    }

    @Override
    public ResponseEntity<TodoList> updateListById(String listId, TodoList todoList) {
        return todoListRepository
                .findById(listId)
                .map(t -> ResponseEntity.ok(todoListRepository.save(t)))
                .orElseGet(() -> ResponseEntity.badRequest().build());
    }

    private Optional<TodoItem> getTodoItem(String listId, String itemId) {
        Optional<TodoList> optionalTodoList = todoListRepository.findById(listId);
        if (optionalTodoList.isEmpty()) {
            return Optional.empty();
        }
        Optional<TodoItem> optionalTodoItem = todoItemRepository.findById(itemId);
        if (optionalTodoItem.isPresent()) {
            TodoItem todoItem = optionalTodoItem.get();
            if (todoItem.getListId().equals(listId)) {
                return Optional.of(todoItem);
            } else {
                return Optional.empty();
            }
        } else {
            return Optional.empty();
        }
    }
}
