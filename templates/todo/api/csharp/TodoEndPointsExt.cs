using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.AspNetCore.Mvc;
using SimpleTodo.Api;

namespace Todo.Api
{
    public static class TodoEndpointsExt
    {
        public static void MapTodoEndpoints(this WebApplication app)
        {

            app.MapGet("/lists", async (ListsRepository _repository, [FromQuery] int? skip = null, [FromQuery] int? batchSize = null) =>
            {
                return TypedResults.Ok(await _repository.GetListsAsync(skip, batchSize));
            })
                .Produces(200)
                .WithOpenApi();


            app.MapPost("/lists", async (ListsRepository _repository, [FromBody] CreateUpdateTodoList list) =>
            {
                var todoList = new TodoList(list.name)
                {
                    Description = list.description
                };

                await _repository.AddListAsync(todoList);

                return Results.Created($"/lists/{todoList.Id}", todoList);
            }).Produces(201)
            .WithOpenApi();


            app.MapGet("/lists/{list_id}", async Task<Results<Ok<TodoList>, NotFound>> (ListsRepository _repository, string list_id) =>
            {
                var list = await _repository.GetListAsync(list_id);

                return list == null ? TypedResults.NotFound() : TypedResults.Ok(list);
            }).Produces(200).Produces(404).WithOpenApi();

            app.MapPut("/lists/{list_id}", async Task<Results<Ok<TodoList>, NotFound>> (ListsRepository _repository, string list_id, [FromBody] CreateUpdateTodoList list) =>
            {
                var existingList = await _repository.GetListAsync(list_id);
                if (existingList == null)
                {
                    return TypedResults.NotFound();
                }

                existingList.Name = list.name;
                existingList.Description = list.description;
                existingList.UpdatedDate = DateTimeOffset.UtcNow;

                await _repository.UpdateList(existingList);

                return TypedResults.Ok(existingList);
            }).Produces(200).WithOpenApi();

            app.MapDelete("/lists/{list_id}", async (ListsRepository _repository, string list_id) => {
                if (await _repository.GetListAsync(list_id) == null)
                {
                    return Results.NotFound();
                }

                await _repository.DeleteListAsync(list_id);

                return Results.NoContent();
            }).Produces(204).WithOpenApi();


            app.MapGet("/lists/{list_id}/items", async (ListsRepository _repository, string list_id, [FromQuery] int? skip = null, [FromQuery] int? batchSize = null) =>
            {
                return TypedResults.Ok(await _repository.GetListItemsAsync(list_id, skip, batchSize));
            }).Produces(200).WithOpenApi();


            app.MapPost("/lists/{list_id}/items", async (ListsRepository _repository, string list_id, [FromBody] CreateUpdateTodoItem item) =>
            {
                var newItem = new TodoItem(list_id, item.name)
                {
                    Name = item.name,
                    Description = item.description,
                    State = item.state,
                    CreatedDate = DateTimeOffset.UtcNow
                };

                await _repository.AddListItemAsync(newItem);
                return Results.CreatedAtRoute($"/lists/{list_id}/items", new { list_id = list_id, item_id = newItem.Id }, newItem);

            }).Produces(201).WithOpenApi();

            app.MapGet("/lists/{list_id}/items/{item_id}", async Task<Results<Ok<TodoItem>, NotFound>> (ListsRepository _repository, string list_id, string item_id) =>
            {
                var item = await _repository.GetListItemAsync(list_id, item_id);

                return item == null ? TypedResults.NotFound() : TypedResults.Ok(item);
            }).Produces(200).WithOpenApi();


            app.MapPut("/lists/{list_id}/items/{item_id}", async Task<Results<Ok<TodoItem>, NotFound>> (ListsRepository _repository, string list_id, string item_id, [FromBody] CreateUpdateTodoItem item) =>
            {
                var existingItem = await _repository.GetListItemAsync(list_id, item_id);
                if (existingItem == null)
                {
                    return TypedResults.NotFound();
                }

                existingItem.Name = item.name;
                existingItem.Description = item.description;
                existingItem.CompletedDate = item.completedDate;
                existingItem.DueDate = item.dueDate;
                existingItem.State = item.state;
                existingItem.UpdatedDate = DateTimeOffset.UtcNow;

                await _repository.UpdateListItem(existingItem);

                return TypedResults.Ok(existingItem);
            }).Produces(200).WithOpenApi();


            app.MapDelete("/lists/{list_id}/items/{item_id}", async (ListsRepository _repository, string list_id, string item_id) =>
            {
                if (await _repository.GetListItemAsync(list_id, item_id) == null)
                {
                    return Results.NotFound();
                }

                await _repository.DeleteListItemAsync(list_id, item_id);

                return Results.NoContent();
            }).Produces(200).Produces(404).WithOpenApi();

            app.MapGet("/lists/{list_id}/state/{state}", async (ListsRepository _repository, string list_id, string state, [FromQuery] int? skip = null, [FromQuery] int? batchSize = null) =>
            {
                return TypedResults.Ok(await _repository.GetListItemsByStateAsync(list_id, state, skip, batchSize));
            }).Produces(200).WithOpenApi();

        }
    }

    public record CreateUpdateTodoList(string name, string? description = null);
    public record CreateUpdateTodoItem(string name, string state, DateTimeOffset? dueDate, DateTimeOffset? completedDate, string? description = null);

}
