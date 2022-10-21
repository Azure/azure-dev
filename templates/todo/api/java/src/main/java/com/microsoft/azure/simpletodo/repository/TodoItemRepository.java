package com.microsoft.azure.simpletodo.repository;

import java.util.List;

import org.springframework.data.mongodb.repository.Aggregation;
import org.springframework.data.mongodb.repository.MongoRepository;
import org.springframework.data.mongodb.repository.Query;

import com.microsoft.azure.simpletodo.model.TodoItem;

public interface TodoItemRepository extends MongoRepository<TodoItem, String> {

    @Query("{ 'listId' : ?0 }")
    List<TodoItem> findTodoItemsByTodoList(String listId);

    @Aggregation(pipeline = {
        "{ '$match': { 'listId' : ?0 } }",
        "{ '$skip': ?1 }",
        "{ '$limit': ?2 }",
    })
    List<TodoItem> findTodoItemsByTodoList(String listId, int skip, int limit);

    @Aggregation(pipeline = {
        "{ '$match': { 'listId' : ?0, 'state' : ?1 } }",
        "{ '$skip': ?2 }",
        "{ '$limit': ?3 }",
    })
    List<TodoItem> findTodoItemsByTodoListAndState(String listId, String state, int skip, int limit);
}
