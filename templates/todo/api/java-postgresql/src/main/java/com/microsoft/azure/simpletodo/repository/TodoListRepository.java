package com.microsoft.azure.simpletodo.repository;

import com.microsoft.azure.simpletodo.model.TodoList;
import org.springframework.data.repository.PagingAndSortingRepository;

public interface TodoListRepository extends PagingAndSortingRepository<TodoList, String> {

}
