package com.microsoft.azure.simpletodo.repository;

import com.microsoft.azure.simpletodo.model.TodoItem;
import java.util.List;
import java.util.Optional;
import org.springframework.data.domain.Pageable;
import org.springframework.data.repository.CrudRepository;
import org.springframework.data.repository.PagingAndSortingRepository;

public interface TodoItemRepository extends PagingAndSortingRepository<TodoItem, String>, CrudRepository<TodoItem, String> {
    void deleteByListIdAndId(String listId, String itemId);

    List<TodoItem> findByListId(String listId);

    Optional<TodoItem> findByListIdAndId(String listId, String id);

    List<TodoItem> findByListId(String listId, Pageable pageable);

    List<TodoItem> findByListIdAndState(String listId, String state, Pageable pageable);
}
