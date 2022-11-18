package com.microsoft.azure.simpletodo.repository;

import com.microsoft.azure.simpletodo.model.TodoList;
import java.util.List;
import org.springframework.data.mongodb.repository.Aggregation;
import org.springframework.data.mongodb.repository.MongoRepository;

public interface TodoListRepository extends MongoRepository<TodoList, String> {
    @Aggregation(pipeline = { "{ '$skip': ?0 }", "{ '$limit': ?1 }" })
    List<TodoList> findAll(int skip, int limit);

    TodoList deleteTodoListById(String id);
}
