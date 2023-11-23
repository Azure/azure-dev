using Microsoft.AspNetCore.Http.HttpResults;
using SimpleTodo.Api;

namespace Todo.Api
{
    public static class TodoEndpointsExt
    {
        public static RouteGroupBuilder MapTodosApi(this RouteGroupBuilder group)
        {
            group.MapGet("/", GetLists);
            group.MapPost("/", CreateList);
            group.MapGet("/{list_id}", GetList);
            group.MapPut("/{list_id}", UpdateList);
            group.MapDelete("/{list_id}", DeleteList);
            group.MapGet("/{list_id}/items", GetListItems);
            group.MapPost("/{list_id}/items", CreateListItem);
            group.MapGet("/{list_id}/items/{item_id}", GetListItem);
            group.MapPut("/{list_id}/items/{item_id}", UpdateListItem);
            group.MapDelete("/{list_id}/items/{item_id}", DeleteListItem);
            group.MapGet("/{list_id}/state/{state}", GetListItemsByState);
            return group;
        }
        public static async Task<Ok<IEnumerable<TodoList>>> GetLists(ListsRepository _repository, int? skip = null, int? batchSize = null)
        {
            return TypedResults.Ok(await _repository.GetListsAsync(skip, batchSize));
        }

        public static async Task<IResult> CreateList(ListsRepository _repository, CreateUpdateTodoList list)
        {
            var todoList = new TodoList(list.name)
            {
                Description = list.description
            };

            await _repository.AddListAsync(todoList);

            return TypedResults.Created($"/lists/{todoList.Id}", todoList);
        }

        public static async Task<IResult> GetList(ListsRepository _repository, string list_id)
        {
            var list = await _repository.GetListAsync(list_id);

            return list == null ? TypedResults.NotFound() : TypedResults.Ok(list);
        }

        public static async Task<IResult> UpdateList(ListsRepository _repository, string list_id, CreateUpdateTodoList list)
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
        }

        public static async Task<IResult> DeleteList(ListsRepository _repository, string list_id)
        {
            if (await _repository.GetListAsync(list_id) == null)
            {
                return TypedResults.NotFound();
            }

            await _repository.DeleteListAsync(list_id);

            return TypedResults.NoContent();
        }

        public static async Task<IResult> GetListItems(ListsRepository _repository, string list_id, int? skip = null, int? batchSize = null)
        {
            if (await _repository.GetListAsync(list_id) == null)
            {
                return TypedResults.NotFound();
            }
            return TypedResults.Ok(await _repository.GetListItemsAsync(list_id, skip, batchSize));
        }

        public static async Task<IResult> CreateListItem(ListsRepository _repository, string list_id, CreateUpdateTodoItem item)
        {
            if (await _repository.GetListAsync(list_id) == null)
            {
                return TypedResults.NotFound();
            }

            var newItem = new TodoItem(list_id, item.name)
            {
                Name = item.name,
                Description = item.description,
                State = item.state,
                CreatedDate = DateTimeOffset.UtcNow
            };

            await _repository.AddListItemAsync(newItem);

            return TypedResults.CreatedAtRoute($"/lists/{list_id}/items{newItem.Id}", newItem);
        }

        public static async Task<IResult> GetListItem(ListsRepository _repository, string list_id, string item_id)
        {
            if (await _repository.GetListAsync(list_id) == null)
            {
                return TypedResults.NotFound();
            }

            var item = await _repository.GetListItemAsync(list_id, item_id);

            return item == null ? TypedResults.NotFound() : TypedResults.Ok(item);
        }

        public static async Task<IResult> UpdateListItem(ListsRepository _repository, string list_id, string item_id, CreateUpdateTodoItem item)
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
        }

        public static async Task<IResult> DeleteListItem(ListsRepository _repository, string list_id, string item_id)
        {
            if (await _repository.GetListItemAsync(list_id, item_id) == null)
            {
                return TypedResults.NotFound();
            }

            await _repository.DeleteListItemAsync(list_id, item_id);

            return TypedResults.NoContent();
        }

        public static async Task<IResult> GetListItemsByState(ListsRepository _repository, string list_id, string state, int? skip = null, int? batchSize = null)
        {
            if (await _repository.GetListAsync(list_id) == null)
            {
                return TypedResults.NotFound();
            }

            return TypedResults.Ok(await _repository.GetListItemsByStateAsync(list_id, state, skip, batchSize));
        }

    }

    public record CreateUpdateTodoList(string name, string? description = null);

    public record CreateUpdateTodoItem(string name, string state, DateTimeOffset? dueDate, DateTimeOffset? completedDate, string? description = null);
}
