package com.microsoft.azure.simpletodo.repository;

import com.microsoft.azure.simpletodo.model.TodoList;
import org.springframework.data.mongodb.repository.MongoRepository;

public interface TodoListRepository extends MongoRepository<TodoList, String> {
}
