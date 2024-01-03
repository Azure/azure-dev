using System.Net;
using Microsoft.Azure.Functions.Worker;
using Microsoft.Azure.Functions.Worker.Http;
using Microsoft.Extensions.Logging;
using System.Text.Json;

namespace SimpleTodo.Api;
public class ListsFunctions
{
    private readonly ILogger logger;
    private readonly ListsRepository repository;

    public ListsFunctions(ILoggerFactory _loggerFactory, ListsRepository _repository)
    {
        logger = _loggerFactory.CreateLogger<ListsFunctions>();
        repository = _repository;
    }

    [Function("GetLists")]
    public async Task<HttpResponseData> GetLists(
        [HttpTrigger(AuthorizationLevel.Anonymous, "get", Route = "lists")]
        HttpRequestData req, int? skip = null, int? batchSize = null)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        var lists = await repository.GetListsAsync(skip, batchSize);
        response.WriteString(JsonSerializer.Serialize(lists));
        return response;
    }

    [Function("CreateList")]
    public async Task<HttpResponseData> CreateList(
       [HttpTrigger(AuthorizationLevel.Anonymous, "post", Route = "lists")] HttpRequestData req, string name, string? description = "")
    {
        var response = req.CreateResponse(HttpStatusCode.Created);
        var todoList = new TodoList(name)
        {
            Description = description
        };
        await repository.AddListAsync(todoList);
        response.WriteString(JsonSerializer.Serialize(todoList));
        return response;
    }

    [Function("GetList")]
    public async Task<HttpResponseData> GetList(
        [HttpTrigger(AuthorizationLevel.Anonymous, "get", Route = "lists/{listId}")] HttpRequestData req, Guid listId)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        var list = await repository.GetListAsync(listId);
        if (list == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        response.WriteString(JsonSerializer.Serialize(list));
        return response;
    }

    [Function("UpdateList")]
    public async Task<HttpResponseData> UpdateList(
       [HttpTrigger(AuthorizationLevel.Anonymous, "put", Route = "lists/{listId}")] HttpRequestData req, Guid listId, string name, string? description = "")
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        var existingList = await repository.GetListAsync(listId);
        if (existingList == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        existingList.Name = name;
        existingList.Description = description;
        existingList.UpdatedDate = DateTimeOffset.UtcNow;
        await repository.SaveChangesAsync();
        response.WriteString(JsonSerializer.Serialize(existingList));
        return response;
    }

    [Function("DeleteList")]
    public async Task<HttpResponseData> DeleteList(
            [HttpTrigger(AuthorizationLevel.Anonymous, "delete", Route = "lists/{listId}")]
        HttpRequestData req, Guid listId)
    {
        var response = req.CreateResponse(HttpStatusCode.NoContent);
        if (await repository.GetListAsync(listId) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        await repository.DeleteListAsync(listId);
        return response;
    }

    [Function("GetListItems")]
    public async Task<HttpResponseData> GetListItems(
        [HttpTrigger(AuthorizationLevel.Anonymous, "get", Route = "lists/{listId}/items")]
        HttpRequestData req, Guid listId, int? skip = null, int? batchSize = null)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        if (await repository.GetListAsync(listId) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        var items = await repository.GetListItemsAsync(listId, skip, batchSize);
        response.WriteString(JsonSerializer.Serialize(items));
        return response;
    }

    [Function("CreateListItem")]
    public async Task<HttpResponseData> CreateListItem(
           [HttpTrigger(AuthorizationLevel.Anonymous, "post", Route = "lists/{listId}/items")] HttpRequestData req,
           Guid listId, string name, string? state = "", string? description = "")
    {
        var response = req.CreateResponse(HttpStatusCode.Created);
        if (await repository.GetListAsync(listId) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        var newItem = new TodoItem(listId, name)
        {
            Name = name,
            Description = description,
            State = (state == null ? "todo" : state),
            CreatedDate = DateTimeOffset.UtcNow
        };
        await repository.AddListItemAsync(newItem);
        response.WriteString(JsonSerializer.Serialize(newItem));
        return response;
    }

    [Function("GetListItem")]
    public async Task<HttpResponseData> GetListItem(
        [HttpTrigger(AuthorizationLevel.Anonymous, "get", Route = "lists/{listId}/items/{itemId}")] HttpRequestData req,
        Guid itemId, Guid listId)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        if (await repository.GetListAsync(listId) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        var item = await repository.GetListItemAsync(listId, itemId);
        response.WriteString(JsonSerializer.Serialize(item));
        return response;
    }

    [Function("UpdateListItem")]
    public async Task<HttpResponseData> UpdateListItem(
       [HttpTrigger(AuthorizationLevel.Anonymous, "put", Route = "lists/{listId}/items/{itemId}")]
       HttpRequestData req, Guid listId, Guid itemId, string name, string? description = "",
       string? state = "", string? completedDate = null, string? dueDate = null)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        var existingItem = await repository.GetListItemAsync(listId, itemId);
        if (existingItem == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        existingItem.Name = name;
        existingItem.Description = description;
        if (completedDate is not null)
        {
            existingItem.CompletedDate = DateTimeOffset.Parse(completedDate);
        }
        if (dueDate is not null)
        {
            existingItem.DueDate = DateTimeOffset.Parse(dueDate); ;
        }
        existingItem.State = state;
        existingItem.UpdatedDate = DateTimeOffset.UtcNow;
        await repository.SaveChangesAsync();
        response.WriteString(JsonSerializer.Serialize(existingItem));
        return response;
    }

    [Function("DeleteListItem")]
    public async Task<HttpResponseData> DeleteListItem(
            [HttpTrigger(AuthorizationLevel.Anonymous, "delete", Route = "lists/{listId}/items/{itemId}")]
        HttpRequestData req, Guid itemId, Guid listId)
    {
        var response = req.CreateResponse(HttpStatusCode.NoContent);
        if (await repository.GetListItemAsync(listId, itemId) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound); ;
        }
        await repository.DeleteListItemAsync(listId, itemId);
        return response;
    }

    [Function("GetListItemsByState")]
    public async Task<HttpResponseData> GetListItemsByState(
        [HttpTrigger(AuthorizationLevel.Anonymous, "get", Route = "lists/{listId}/state/{state}")]
        HttpRequestData req, Guid listId, string state, int? skip = null, int? batchSize = null)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        if (await repository.GetListAsync(listId) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        var items = await repository.GetListItemsByStateAsync(listId, state, skip, batchSize);
        response.WriteString(JsonSerializer.Serialize(items));
        return response;
    }
}