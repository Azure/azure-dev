package com.microsoft.azure.simpletodo.repository;

import com.microsoft.azure.simpletodo.model.TodoItem;
import java.util.List;
import java.util.Optional;
import org.springframework.data.mongodb.repository.Aggregation;
import org.springframework.data.mongodb.repository.MongoRepository;

public interface TodoItemRepository extends MongoRepository<TodoItem, String> {
    TodoItem deleteTodoItemByListIdAndId(String listId, String itemId);

    List<TodoItem> findTodoItemsByListId(String listId);

    Optional<TodoItem> findTodoItemByListIdAndId(String listId, String id);

    @Aggregation(pipeline = { "{ '$match': { 'listId' : ?0 } }", "{ '$skip': ?1 }", "{ '$limit': ?2 }" })
    List<TodoItem> findTodoItemsByTodoList(String listId, int skip, int limit);

    @Aggregation(pipeline = { "{ '$match': { 'listId' : ?0, 'state' : ?1 } }", "{ '$skip': ?2 }", "{ '$limit': ?3 }" })
    List<TodoItem> findTodoItemsByTodoListAndState(String listId, String state, int skip, int limit);
}
