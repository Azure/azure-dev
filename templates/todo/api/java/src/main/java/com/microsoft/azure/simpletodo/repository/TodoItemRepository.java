package com.microsoft.azure.simpletodo.repository;

import com.microsoft.azure.simpletodo.model.TodoItem;
import org.springframework.data.domain.Pageable;
import org.springframework.data.mongodb.repository.MongoRepository;
import org.springframework.data.mongodb.repository.Query;

import java.util.List;

public interface TodoItemRepository extends MongoRepository<TodoItem, String> {

    @Query("{ 'listId' : ?0 }")
    List<TodoItem> findTodoItemsByTodoList(String listId, Pageable pageable);

    @Query("{ 'listId' : ?0, 'state' : ?1 }")
    List<TodoItem> findTodoItemsByTodoListAndState(String listId, String state, Pageable pageable);
}
