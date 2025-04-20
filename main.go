package main

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"database/sql"
	"path/filepath"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
)

var db *sql.DB

type Recipe struct {
	ID          int
	Title       string
	Description string
	Time        string
	ImagePath   string
}

func main() {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL is not set")
	}
	var err error
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("DB connect:", err)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatal("DB ping:", err)
	}

	r := mux.NewRouter()

	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	r.PathPrefix("/uploads/").Handler(http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))))

	r.HandleFunc("/", homeHandler)
	r.HandleFunc("/recipes", listRecipesHandler)

	r.HandleFunc("/recipes/new", newRecipeFormHandler).Methods("GET")
	r.HandleFunc("/recipes/new", createRecipeHandler).Methods("POST")

	r.HandleFunc("/recipes/{id}/edit", editRecipeFormHandler).Methods("GET")
	r.HandleFunc("/recipes/{id}/edit", updateRecipeHandler).Methods("POST")

	r.HandleFunc("/recipes/{id}", showRecipeHandler)

	log.Println("Server started at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", r))
}

func renderTemplate(w http.ResponseWriter, tmpl string, data interface{}) {
	t, err := template.ParseFiles(
		"templates/base.html",
		"templates/"+tmpl+".html",
	)
	if err != nil {
		http.Error(w, "Error loading template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	err = t.ExecuteTemplate(w, "base", data)
	if err != nil {
		http.Error(w, "Error executing template: "+err.Error(), http.StatusInternalServerError)
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "home", nil)
}

func listRecipesHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT id,title,description,time,image_path FROM recipes ORDER BY id`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var recs []Recipe
	for rows.Next() {
		var rc Recipe
		if err := rows.Scan(&rc.ID, &rc.Title, &rc.Description, &rc.Time, &rc.ImagePath); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		recs = append(recs, rc)
	}
	renderTemplate(w, "recipes", recs)
}

func showRecipeHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	rc := Recipe{ID: id}
	err := db.QueryRow(
		`SELECT title,description,time,image_path FROM recipes WHERE id=$1`, id,
	).Scan(&rc.Title, &rc.Description, &rc.Time, &rc.ImagePath)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	renderTemplate(w, "recipe_detail", rc)
}

func newRecipeFormHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "recipe_form", nil)
}

func createRecipeHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Could not parse form", http.StatusBadRequest)
		return
	}
	title := r.FormValue("title")
	timeStr := r.FormValue("time")
	desc := r.FormValue("description")

	// Try to get uploaded file
	file, header, err := r.FormFile("image")
	imgPath := "" // empty so SQL default happens
	if err == nil {
		defer file.Close()
		os.MkdirAll("uploads/images", 0755)
		fn := fmt.Sprintf("%d_%s", time.Now().UnixNano(), header.Filename)
		disk := filepath.Join("uploads", "images", fn)
		url := "/uploads/images/" + fn

		dst, _ := os.Create(disk)
		defer dst.Close()
		io.Copy(dst, file)
		imgPath = url
	} else if err != http.ErrMissingFile {
		http.Error(w, "Error reading image", http.StatusBadRequest)
		return
	}

	var newID int
	err = db.QueryRow(
		`INSERT INTO recipes (title,description,time,image_path)
           VALUES ($1,$2,$3,$4) RETURNING id`,
		title, desc, timeStr, imgPath,
	).Scan(&newID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/recipes/%d", newID), http.StatusSeeOther)
}

func editRecipeFormHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var rc Recipe
	rc.ID = id
	if err := db.QueryRow(
		`SELECT title, description, time, image_path
           FROM recipes WHERE id=$1`, id,
	).Scan(&rc.Title, &rc.Description, &rc.Time, &rc.ImagePath); err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	renderTemplate(w, "recipe_edit", rc)
}

func updateRecipeHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.Error(w, "Invalid recipe ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Could not parse form", http.StatusBadRequest)
		return
	}

	title := r.FormValue("title")
	timeStr := r.FormValue("time")
	desc := r.FormValue("description")

	// see if there's a new image
	imagePath := ""
	file, header, ferr := r.FormFile("image")
	if ferr == nil {
		defer file.Close()
		os.MkdirAll("uploads/images", 0755)
		filename := fmt.Sprintf("%d_%s", id, header.Filename)
		diskPath := filepath.Join("uploads", "images", filename)
		urlPath := "/uploads/images/" + filename

		dst, err := os.Create(diskPath)
		if err != nil {
			http.Error(w, "Could not save image", http.StatusInternalServerError)
			return
		}
		defer dst.Close()
		if _, err := io.Copy(dst, file); err != nil {
			http.Error(w, "Failed writing image", http.StatusInternalServerError)
			return
		}
		imagePath = urlPath
	} else if ferr != http.ErrMissingFile {
		http.Error(w, "Error processing image upload", http.StatusBadRequest)
		return
	}

	// build conditional UPDATE
	if imagePath != "" {
		_, err = db.Exec(
			`UPDATE recipes
               SET title=$1, description=$2, time=$3, image_path=$4
             WHERE id=$5`,
			title, desc, timeStr, imagePath, id,
		)
	} else {
		_, err = db.Exec(
			`UPDATE recipes
               SET title=$1, description=$2, time=$3
             WHERE id=$4`,
			title, desc, timeStr, id,
		)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/recipes/%d", id), http.StatusSeeOther)
}
