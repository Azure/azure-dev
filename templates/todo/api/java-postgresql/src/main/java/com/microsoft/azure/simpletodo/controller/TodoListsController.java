package com.microsoft.azure.simpletodo.controller;

import com.microsoft.azure.simpletodo.api.ListsApi;
import com.microsoft.azure.simpletodo.model.TodoList;
import com.microsoft.azure.simpletodo.repository.TodoListRepository;
import java.math.BigDecimal;
import java.net.URI;
import java.util.List;
import java.util.Optional;
import jakarta.validation.constraints.NotNull;
import org.springframework.data.domain.PageRequest;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.servlet.support.ServletUriComponentsBuilder;

@RestController
public class TodoListsController implements ListsApi {

    private final TodoListRepository todoListRepository;

    public TodoListsController(TodoListRepository todoListRepository) {
        this.todoListRepository = todoListRepository;
    }

    public ResponseEntity<TodoList> createList(TodoList todoList) {
        final TodoList savedTodoList = todoListRepository.save(todoList);
        URI location = ServletUriComponentsBuilder
            .fromCurrentRequest()
            .path("/{id}")
            .buildAndExpand(savedTodoList.getId())
            .toUri();
        return ResponseEntity.created(location).body(savedTodoList);
    }

    public ResponseEntity<Void> deleteListById(String listId) {
        Optional<TodoList> todoList = todoListRepository.findById(listId);
        if (todoList.isPresent()) {
            todoListRepository.deleteById(listId);
            return ResponseEntity.noContent().build();
        } else {
            return ResponseEntity.notFound().build();
        }
    }

    public ResponseEntity<TodoList> getListById(String listId) {
        return todoListRepository
            .findById(listId)
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    public ResponseEntity<List<TodoList>> getLists(BigDecimal top, BigDecimal skip) {
        // no need to check nullity of top and skip, because they have default values.
        return ResponseEntity.ok(todoListRepository.findAll(PageRequest.of(skip.intValue(), top.intValue())).getContent());
    }

    public ResponseEntity<TodoList> updateListById(String listId, @NotNull TodoList todoList) {
        // make sure listId is set into the todoItem, otherwise it will create a new todo
        // list.
        todoList.setId(listId);
        return todoListRepository
            .findById(listId)
            .map(t -> ResponseEntity.ok(todoListRepository.save(todoList)))
            .orElseGet(() -> ResponseEntity.notFound().build());
    }
}
