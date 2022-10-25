using System.ComponentModel.DataAnnotations;
using System.Text.Json.Serialization;

namespace SimpleTodo.Api;

public class TodoList
{
    public TodoList(string name)
    {
        Name = name;
    }

    [Key]
    [JsonPropertyName("id")]
    public Guid? Id { get; set; }
    [JsonPropertyName("name")]
    public string Name { get; set; }
    
    [JsonPropertyName("description")]
    public string? Description { get; set; }
    
    [JsonPropertyName("createdDate")]
    public DateTimeOffset CreatedDate { get; set; } = System.DateTimeOffset.UtcNow;
    
    [JsonPropertyName("updatedDate")]
    public DateTimeOffset? UpdatedDate { get; set; }
}