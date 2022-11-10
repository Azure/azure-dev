package com.microsoft.azure.simpletodo.web;

import com.microsoft.azure.simpletodo.api.ListsApi;
import com.microsoft.azure.simpletodo.model.TodoList;
import com.microsoft.azure.simpletodo.repository.TodoListRepository;
import lombok.RequiredArgsConstructor;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.servlet.support.ServletUriComponentsBuilder;

import javax.validation.constraints.NotNull;
import java.math.BigDecimal;
import java.net.URI;
import java.util.List;

@RestController
@RequiredArgsConstructor
public class TodoListsController implements ListsApi {
    private final TodoListRepository todoListRepository;

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
        return todoListRepository.findById(listId)
            .map(l -> todoListRepository.deleteTodoListById(l.getId()))
            .map(l -> ResponseEntity.noContent().<Void>build())
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    public ResponseEntity<TodoList> getListById(String listId) {
        return todoListRepository.findById(listId)
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    public ResponseEntity<List<TodoList>> getLists(BigDecimal top, BigDecimal skip) {
        return ResponseEntity.ok(todoListRepository.findAll(skip.intValue(), top.intValue()));
    }

    public ResponseEntity<TodoList> updateListById(String listId, @NotNull TodoList todoList) {
        todoList.setId(listId);
        return todoListRepository.findById(listId)
            .map(t -> ResponseEntity.ok(todoListRepository.save(todoList)))
            .orElseGet(() -> ResponseEntity.notFound().build());
    }
}
