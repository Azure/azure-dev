using MongoDB.Bson;
using MongoDB.Bson.Serialization;
using MongoDB.Bson.Serialization.Conventions;
using MongoDB.Bson.Serialization.IdGenerators;
using MongoDB.Bson.Serialization.Serializers;
using MongoDB.Driver;

namespace SimpleTodo.Api;

public class ListsRepository
{
    private readonly IMongoCollection<TodoList> _listsCollection;
    private readonly IMongoCollection<TodoItem> _itemsCollection;

    static ListsRepository()
    {
        var conventionPack = new ConventionPack
        {
            new CamelCaseElementNameConvention()
        };

        ConventionRegistry.Register(
            name: "Camel case",
            conventions: conventionPack,
            filter: t => t == typeof(TodoList) || t == typeof(TodoItem));

        var objectIdSerializer = new StringSerializer(BsonType.ObjectId);
        BsonClassMap.RegisterClassMap<TodoList>(map =>
        {
            map.AutoMap();
            map.MapIdProperty(item => item.Id)
                .SetIgnoreIfDefault(true)
                .SetIdGenerator(StringObjectIdGenerator.Instance)
                .SetSerializer(objectIdSerializer);
        });

        BsonClassMap.RegisterClassMap<TodoItem>(map =>
        {
            map.AutoMap();
            map.MapIdProperty(item => item.Id)
                .SetIgnoreIfDefault(true)
                .SetIdGenerator(StringObjectIdGenerator.Instance)
                .SetSerializer(objectIdSerializer);
            map.MapProperty(item => item.ListId).SetSerializer(objectIdSerializer);
        });
    }

    public ListsRepository(MongoClient client, IConfiguration configuration)
    {
        var database = client.GetDatabase(configuration["AZURE_COSMOS_DATABASE_NAME"]);
        _listsCollection = database.GetCollection<TodoList>("TodoList");
        _itemsCollection = database.GetCollection<TodoItem>("TodoItem");
    }

    public async Task<IEnumerable<TodoList>> GetListsAsync(int? skip, int? batchSize)
    {
        var cursor = await _listsCollection.FindAsync(
            _ => true,
            new FindOptions<TodoList>()
            {
                Skip = skip,
                BatchSize = batchSize
            });
        return await cursor.ToListAsync();
    }

    public async Task<TodoList?> GetListAsync(string listId)
    {
        var cursor = await _listsCollection.FindAsync(list => list.Id == listId);
        return await cursor.FirstOrDefaultAsync();
    }

    public async Task DeleteListAsync(string listId)
    {
        await _listsCollection.DeleteOneAsync(list => list.Id == listId);
    }

    public async Task AddListAsync(TodoList list)
    {
        await _listsCollection.InsertOneAsync(list);
    }

    public async Task UpdateList(TodoList existingList)
    {
        await _listsCollection.ReplaceOneAsync(list => list.Id == existingList.Id, existingList);
    }

    public async Task<IEnumerable<TodoItem>> GetListItemsAsync(string listId, int? skip, int? batchSize)
    {
        var cursor = await _itemsCollection.FindAsync(
            item => item.ListId == listId,
            new FindOptions<TodoItem>()
            {
                Skip = skip,
                BatchSize = batchSize
            });
        return await cursor.ToListAsync();
    }

    public async Task<IEnumerable<TodoItem>> GetListItemsByStateAsync(string listId, string state, int? skip, int? batchSize)
    {
        var cursor = await _itemsCollection.FindAsync(
            item => item.ListId == listId && item.State == state,
            new FindOptions<TodoItem>()
            {
                Skip = skip,
                BatchSize = batchSize
            });
        return await cursor.ToListAsync();
    }

    public async Task AddListItemAsync(TodoItem item)
    {
        await _itemsCollection.InsertOneAsync(item);
    }

    public async Task<TodoItem?> GetListItemAsync(string listId, string itemId)
    {
        var cursor = await _itemsCollection.FindAsync(item => item.Id == itemId && item.ListId == listId);
        return await cursor.FirstOrDefaultAsync();
    }

    public async Task DeleteListItemAsync(string listId, string itemId)
    {
        await _itemsCollection.DeleteOneAsync(item => item.Id == itemId && item.ListId == listId);
    }

    public async Task UpdateListItem(TodoItem existingItem)
    {
        await _itemsCollection.ReplaceOneAsync(item => item.Id == existingItem.Id, existingItem);
    }
}