FROM python:3.10
WORKDIR /code
EXPOSE 3100
COPY ./requirements.txt /code/requirements.txt

RUN pip install --no-cache-dir --upgrade -r /code/requirements.txt

COPY ./todo /code/todo

CMD ["uvicorn", "todo.app:app", "--host", "0.0.0.0", "--port", "3100", "--proxy-headers"]
