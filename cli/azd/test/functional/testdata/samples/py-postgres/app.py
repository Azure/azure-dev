import os
import psycopg2
from flask import Flask

app = Flask(__name__)

def get_db_connection():
    conn = psycopg2.connect(host=os.environ['DB_HOST'],
                            database=os.environ['DB_NAME'],
                            user=os.environ['DB_USER'],
                            password=os.environ['DB_PASSWORD'])
    return conn


@app.route('/')
def index():
    conn = get_db_connection()
    cur = conn.cursor()
    cur.execute('SELECT 1;')
    cur.fetchall()
    cur.close()
    conn.close()
    return "Hello, `azd`."
