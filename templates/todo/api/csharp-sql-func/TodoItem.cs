using System.ComponentModel.DataAnnotations;
using System.Text.Json.Serialization;

namespace SimpleTodo.Api;

public class TodoItem
{
    public TodoItem(Guid listId, string name)
    {
        ListId = listId;
        Name = name;
    }
    [Key]
    [JsonPropertyName("id")]
    public Guid? Id { get; set; }
    
    [JsonPropertyName("listId")]
    public Guid ListId { get; set; }

    [JsonPropertyName("name")]
    public string Name { get; set; }

    [JsonPropertyName("description")]
    public string? Description { get; set; }

    [JsonPropertyName("state")]
    public string State { get; set; } = "todo";

    [JsonPropertyName("dueDate")]
    public DateTimeOffset? DueDate { get; set; }

    [JsonPropertyName("completedDate")]
    public DateTimeOffset? CompletedDate { get; set; }

    [JsonPropertyName("createdDate")]
    public DateTimeOffset? CreatedDate { get; set; } = DateTimeOffset.UtcNow;

    [JsonPropertyName("updatedDate")]
    public DateTimeOffset? UpdatedDate { get; set; }
}