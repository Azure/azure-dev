namespace SimpleTodo.Api;

public class TodoItem
{
    public TodoItem(string listId, string name)
    {
        ListId = listId;
        Name = name;
    }

    public string? Id { get; set; }
    public string ListId { get; set; }
    public string Name { get; set; }
    public string? Description { get; set; }
    public string State { get; set; } = "todo";
    public DateTimeOffset? DueDate { get; set; }
    public DateTimeOffset? CompletedDate { get; set; }
    public DateTimeOffset? CreatedDate { get; set; } = DateTimeOffset.UtcNow;
    public DateTimeOffset? UpdatedDate { get; set; }
}