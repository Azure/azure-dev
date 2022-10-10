using System.Net;
using Microsoft.Azure.Functions.Worker;
using Microsoft.Azure.Functions.Worker.Http;
using Microsoft.Extensions.Logging;
using System.Text.Json;

namespace SimpleTodo.Api;
public class ListsFunctions
{
    private readonly ILogger _logger;
    private readonly ListsRepository _repository;

    public ListsFunctions(ILoggerFactory loggerFactory, ListsRepository repository)
    {
        _logger = loggerFactory.CreateLogger<ListsFunctions>();
        _repository = repository;
    }

    [Function("GetLists")]
    public async Task<HttpResponseData> GetLists(
        [HttpTrigger(AuthorizationLevel.Anonymous, "get", Route = "lists")]
        HttpRequestData req, int? skip, int? batchSize)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        var lists = await _repository.GetListsAsync(skip, batchSize);
        response.WriteString(JsonSerializer.Serialize(lists));
        return response;
    }

    [Function("CreateList")]
    public async Task<HttpResponseData> CreateList(
       [HttpTrigger(AuthorizationLevel.Anonymous, "post", Route = "lists")] HttpRequestData req, string list_id, string name, string? description = null)
    {
        var response = req.CreateResponse(HttpStatusCode.Created);
        var todoList = new TodoList(name)
        {
            Description = description
        };
        await _repository.AddListAsync(todoList);
        response.WriteString(JsonSerializer.Serialize(todoList));
        return response;
    }

    [Function("GetList")]
    public async Task<HttpResponseData> GetList(
        [HttpTrigger(AuthorizationLevel.Anonymous, "get", Route = "lists/{list_id}")] HttpRequestData req, Guid list_id)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        var list = await _repository.GetListAsync(list_id);
        if (list == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        response.WriteString(JsonSerializer.Serialize(list));
        return response;
    }

    [Function("UpdateList")]
    public async Task<HttpResponseData> UpdateList(
       [HttpTrigger(AuthorizationLevel.Anonymous, "put", Route = "lists/{list_id}")] HttpRequestData req, Guid list_id, string name, string? description)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        var existingList = await _repository.GetListAsync(list_id);
        if (existingList == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        existingList.Name = name;
        existingList.Description = description;
        existingList.UpdatedDate = DateTimeOffset.UtcNow;
        await _repository.SaveChangesAsync();
        response.WriteString(JsonSerializer.Serialize(existingList));
        return response;
    }

    [Function("DeleteList")]
    public async Task<HttpResponseData> DeleteList(
            [HttpTrigger(AuthorizationLevel.Anonymous, "delete", Route = "lists/{list_id}")]
        HttpRequestData req, Guid list_id)
    {
        var response = req.CreateResponse(HttpStatusCode.NoContent);
        if (await _repository.GetListAsync(list_id) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        await _repository.DeleteListAsync(list_id);
        return response;
    }

    [Function("GetListItems")]
    public async Task<HttpResponseData> GetListItems(
        [HttpTrigger(AuthorizationLevel.Anonymous, "get", Route = "lists/{list_id}/items")]
        HttpRequestData req, Guid list_id, int? skip, int? batchSize)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        if (await _repository.GetListAsync(list_id) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        var items = await _repository.GetListItemsAsync(list_id, skip, batchSize);
        response.WriteString(JsonSerializer.Serialize(items));
        return response;
    }

    [Function("CreateListItem")]
    public async Task<HttpResponseData> CreateListItem(
           [HttpTrigger(AuthorizationLevel.Anonymous, "post", Route = "lists/{list_id}/items")] HttpRequestData req,
           Guid list_id, string name, string? state, string? description)
    {
        var response = req.CreateResponse(HttpStatusCode.Created);
        if (await _repository.GetListAsync(list_id) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        var newItem = new TodoItem(list_id, name)
        {
            Name = name,
            Description = description,
            State = (state == null ? "todo" : state),
            CreatedDate = DateTimeOffset.UtcNow
        };
        await _repository.AddListItemAsync(newItem);
        response.WriteString(JsonSerializer.Serialize(newItem));
        return response;
    }

    [Function("GetListItem")]
    public async Task<HttpResponseData> GetListItem(
        [HttpTrigger(AuthorizationLevel.Anonymous, "get", Route = "lists/{list_id}/items/{item_id}")] HttpRequestData req,
        Guid item_id, Guid list_id)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        if (await _repository.GetListAsync(list_id) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        var item = await _repository.GetListItemAsync(list_id, item_id);
        response.WriteString(JsonSerializer.Serialize(item));
        return response;
    }

    [Function("UpdateListItem")]
    public async Task<HttpResponseData> UpdateListItem(
       [HttpTrigger(AuthorizationLevel.Anonymous, "put", Route = "lists/{list_id}/items/{item_id}")]
       HttpRequestData req, Guid list_id, Guid item_id, string name, string? description,
       string state, string? completedDate, string? dueDate)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        var existingItem = await _repository.GetListItemAsync(list_id, item_id);
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
        await _repository.SaveChangesAsync();
        response.WriteString(JsonSerializer.Serialize(existingItem));
        return response;
    }

    [Function("DeleteListItem")]
    public async Task<HttpResponseData> DeleteListItem(
            [HttpTrigger(AuthorizationLevel.Anonymous, "delete", Route = "lists/{list_id}/items/{item_id}")]
        HttpRequestData req, Guid item_id, Guid list_id)
    {
        var response = req.CreateResponse(HttpStatusCode.NoContent);
        if (await _repository.GetListItemAsync(list_id, item_id) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound); ;
        }
        await _repository.DeleteListItemAsync(list_id, item_id);
        return response;
    }

    [Function("GetListItemsByState")]
    public async Task<HttpResponseData> GetListItemsByState(
        [HttpTrigger(AuthorizationLevel.Anonymous, "get", Route = "lists/{list_id}/state/{state}")]
        HttpRequestData req, Guid list_id, string state, int? skip = null, int? batchSize = null)
    {
        var response = req.CreateResponse(HttpStatusCode.OK);
        if (await _repository.GetListAsync(list_id) == null)
        {
            return req.CreateResponse(HttpStatusCode.NotFound);
        }
        var items = await _repository.GetListItemsByStateAsync(list_id, state, skip, batchSize);
        response.WriteString(JsonSerializer.Serialize(items));
        return response;
    }
}