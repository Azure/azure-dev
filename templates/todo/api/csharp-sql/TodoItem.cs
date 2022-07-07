using System.ComponentModel.DataAnnotations;

namespace SimpleTodo.Api;

public class TodoItem
{
    public TodoItem(Guid listId, string name)
    {
        ListId = listId;
        Name = name;
    }

    [Key]
    public Guid? Id { get; set; }

    public TodoList? List { get; set; }
    public Guid ListId { get; set; }
    public string Name { get; set; }
    public string? Description { get; set; }
    public string State { get; set; } = "todo";
    public DateTimeOffset? DueDate { get; set; }
    public DateTimeOffset? CompletedDate { get; set; }
    public DateTimeOffset? CreatedDate { get; set; } = DateTimeOffset.UtcNow;
    public DateTimeOffset? UpdatedDate { get; set; }
}