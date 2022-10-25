using Microsoft.EntityFrameworkCore;

namespace SimpleTodo.Api;

public class ListsRepository
{

    private readonly TodoDb _db;

    public ListsRepository(TodoDb db)
    {
        _db = db;
    }

     public async Task<IEnumerable<TodoList>> GetListsAsync(int? skip, int? batchSize)
    {
        return await ToListAsync(_db.Lists, skip, batchSize);
    }

    public async Task<TodoList?> GetListAsync(Guid listId)
    {
        return await _db.Lists.SingleOrDefaultAsync(list => list.Id == listId);
    }

    public async Task DeleteListAsync(Guid listId)
    {
        var list = await GetListAsync(listId);
        if (list != null)
        {
            _db.Lists.Remove(list);
        }

        await _db.SaveChangesAsync();
    }

    public async Task AddListAsync(TodoList list)
    {
        _db.Lists.Add(list);
        await _db.SaveChangesAsync();
    }
    public async Task SaveChangesAsync()
    {
        await _db.SaveChangesAsync();
    }

    public async Task<IEnumerable<TodoItem>> GetListItemsAsync(Guid listId, int? skip, int? batchSize)
    {
        return await ToListAsync(
            _db.Items.Where(i => i.ListId == listId),
            skip,
            batchSize);
    }

    public async Task<IEnumerable<TodoItem>> GetListItemsByStateAsync(Guid listId, string state, int? skip, int? batchSize)
    {
        return await ToListAsync(
            _db.Items.Where(i => i.ListId == listId && i.State == state),
            skip,
            batchSize);
    }

    public async Task AddListItemAsync(TodoItem item)
    {
        _db.Items.Add(item);
        await _db.SaveChangesAsync();
    }

    public async Task<TodoItem?> GetListItemAsync(Guid listId, Guid itemId)
    {
        return await _db.Items.SingleOrDefaultAsync(item => item.Id == itemId && item.ListId == listId);
    }

    public async Task DeleteListItemAsync(Guid listId, Guid itemId)
    {
        var list = await GetListItemAsync(listId, itemId);
        if (list != null)
        {
            _db.Items.Remove(list);
        }

        await _db.SaveChangesAsync();
    }
    private async Task<List<T>> ToListAsync<T>(IQueryable<T> queryable, int? skip, int? batchSize)
    {
        if (skip != null)
        {
            queryable = queryable.Skip(skip.Value);
        }

        if (batchSize != null)
        {
            queryable = queryable.Take(batchSize.Value);
        }
        return await queryable.ToListAsync();
    }
}