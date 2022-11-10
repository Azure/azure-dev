package com.microsoft.azure.simpletodo.controller;

import com.microsoft.azure.simpletodo.api.ListsApi;
import com.microsoft.azure.simpletodo.model.TodoList;
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
import javax.validation.constraints.NotNull;
import java.math.BigDecimal;
import java.net.URI;
import java.util.List;

@RequestMapping("/lists")
@RestController
@RequiredArgsConstructor
@Validated
public class TodoListsController implements ListsApi {
    private final TodoListRepository todoListRepository;

    @RequestMapping(
        method = RequestMethod.POST,
        produces = MediaType.APPLICATION_JSON_VALUE,
        consumes = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoList> createList(
        @Valid @RequestBody(required = false) TodoList todoList
    ) {
        final TodoList savedTodoList = todoListRepository.save(todoList);
        URI location = ServletUriComponentsBuilder
            .fromCurrentRequest()
            .path("/{id}")
            .buildAndExpand(savedTodoList.getId())
            .toUri();
        return ResponseEntity.created(location).body(savedTodoList);
    }

    @RequestMapping(
        method = RequestMethod.DELETE,
        value = "/{listId}"
    )
    public ResponseEntity<Void> deleteListById(
        @PathVariable("listId") String listId
    ) {
        return todoListRepository.findById(listId)
            .map(l -> todoListRepository.deleteTodoListById(l.getId()))
            .map(l -> ResponseEntity.noContent().<Void>build())
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @RequestMapping(
        method = RequestMethod.GET,
        value = "/{listId}",
        produces = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoList> getListById(
        @PathVariable("listId") String listId
    ) {
        return todoListRepository.findById(listId)
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @RequestMapping(
        method = RequestMethod.GET,
        produces = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<List<TodoList>> getLists(
        @Valid @RequestParam(value = "top", required = false, defaultValue = "20") BigDecimal top,
        @Valid @RequestParam(value = "skip", required = false, defaultValue = "0") BigDecimal skip
    ) {
        return ResponseEntity.ok(todoListRepository.findAll(skip.intValue(), top.intValue()));
    }

    @RequestMapping(
        method = RequestMethod.PUT,
        value = "/{listId}",
        produces = MediaType.APPLICATION_JSON_VALUE,
        consumes = MediaType.APPLICATION_JSON_VALUE
    )
    public ResponseEntity<TodoList> updateListById(
        @PathVariable(value = "listId") String listId,
        @Valid @RequestBody @NotNull TodoList todoList
    ) {
        todoList.setId(listId);
        return todoListRepository.findById(listId)
            .map(t -> ResponseEntity.ok(todoListRepository.save(todoList)))
            .orElseGet(() -> ResponseEntity.notFound().build());
    }
}
