package com.microsoft.azure.simpletodo.web;

import com.microsoft.azure.simpletodo.api.ListsApi;
import com.microsoft.azure.simpletodo.model.TodoItem;
import com.microsoft.azure.simpletodo.model.TodoList;
import com.microsoft.azure.simpletodo.model.TodoState;
import com.microsoft.azure.simpletodo.repository.TodoItemRepository;
import com.microsoft.azure.simpletodo.repository.TodoListRepository;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.servlet.support.ServletUriComponentsBuilder;

import javax.validation.constraints.NotNull;
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

    @Override
    public ResponseEntity<TodoList> createList(TodoList todoList) {
        final TodoList savedTodoList = todoListRepository.save(todoList);
        URI location = ServletUriComponentsBuilder
            .fromCurrentRequest()
            .path("/{id}")
            .buildAndExpand(savedTodoList.getId())
            .toUri();
        return ResponseEntity.created(location).body(savedTodoList);
    }

    @Override
    public ResponseEntity<Void> deleteItemById(String listId, String itemId) {
        return todoItemRepository.findTodoItemByListIdAndId(listId, itemId)
            .map(i -> todoItemRepository.deleteTodoItemByListIdAndId(i.getListId(), i.getId()))
            .map(i -> ResponseEntity.noContent().<Void>build())
            .orElse(ResponseEntity.notFound().build());
    }

    @Override
    public ResponseEntity<Void> deleteListById(String listId) {
        return todoListRepository.findById(listId)
            .map(l -> todoListRepository.deleteTodoListById(l.getId()))
            .map(l -> ResponseEntity.noContent().<Void>build())
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @Override
    public ResponseEntity<TodoItem> getItemById(String listId, String itemId) {
        return todoItemRepository.findTodoItemByListIdAndId(listId, itemId)
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @Override
    public ResponseEntity<List<TodoItem>> getItemsByListId(String listId, BigDecimal top, BigDecimal skip) {
        return todoListRepository.findById(listId)
            .map(l -> todoItemRepository.findTodoItemsByTodoList(l.getId(), skip.intValue(), top.intValue()))
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @Override
    public ResponseEntity<List<TodoItem>> getItemsByListIdAndState(String listId, TodoState state, BigDecimal top, BigDecimal skip) {
        return todoListRepository.findById(listId)
            .map(l -> todoItemRepository.findTodoItemsByTodoListAndState(l.getId(), state.name(), skip.intValue(), top.intValue()))
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @Override
    public ResponseEntity<TodoList> getListById(String listId) {
        return todoListRepository.findById(listId)
            .map(ResponseEntity::ok)
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @Override
    public ResponseEntity<List<TodoList>> getLists(BigDecimal top, BigDecimal skip) {
        return ResponseEntity.ok(todoListRepository.findAll(skip.intValue(), top.intValue()));
    }

    @Override
    public ResponseEntity<TodoItem> updateItemById(String listId, String itemId, TodoItem todoItem) {
        todoItem.setId(itemId);
        todoItem.setListId(listId);
        return todoItemRepository.findTodoItemByListIdAndId(listId, itemId)
            .map(t -> todoItemRepository.save(todoItem))
            .map(ResponseEntity::ok) // return the saved item.
            .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @Override
    public ResponseEntity<Void> updateItemsStateByListId(String listId, TodoState state) {
        final List<TodoItem> items = todoItemRepository.findTodoItemsByListId(listId);
        items.forEach(item -> item.setState(state));
        todoItemRepository.saveAll(items); // save items in batch.
        return ResponseEntity.noContent().build();
    }

    @Override
    public ResponseEntity<TodoList> updateListById(String listId, @NotNull TodoList todoList) {
        todoList.setId(listId);
        return todoListRepository.findById(listId)
            .map(t -> ResponseEntity.ok(todoListRepository.save(todoList)))
            .orElseGet(() -> ResponseEntity.notFound().build());
    }
}
