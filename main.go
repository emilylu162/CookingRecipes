package main

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"
)

type Recipe struct {
	ID          int
	Title       string
	Description string
	Time        string
	ImagePath   string
}

var recipes = []Recipe{
	{ID: 1, Title: "Shin Ramen", Description: "Your favorite ramen...but upgraded", Time: "10 minutes", ImagePath: "./uploads/images/shin.jpg"},
	{ID: 2, Title: "Shrimp Dumplings", Description: "Classic dumplings without the carbs", Time: "15 minutes"},
}

func main() {
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
	renderTemplate(w, "recipes", recipes)
}

func showRecipeHandler(w http.ResponseWriter, r *http.Request) {
	idStr := mux.Vars(r)["id"]
	id, _ := strconv.Atoi(idStr)
	for _, recipe := range recipes {
		if recipe.ID == id {
			renderTemplate(w, "recipe_detail", recipe)
			return
		}
	}
	http.NotFound(w, r)
}

func newRecipeFormHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "recipe_form", nil)
}

func createRecipeHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(10 << 20) // 10 MB max memory
	if err != nil {
		http.Error(w, "Could not parse form", http.StatusBadRequest)
		return
	}

	title := r.FormValue("title")
	time := r.FormValue("time")
	description := r.FormValue("description")
	imagePath := ""

	// Handle image upload
	file, handler, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		fileName := fmt.Sprintf("%d_%s", len(recipes)+1, handler.Filename)
		imagePath = "uploads/images/" + fileName

		dst, err := os.Create(imagePath)
		if err != nil {
			http.Error(w, "Could not save image", http.StatusInternalServerError)
			return
		}
		defer dst.Close()
		io.Copy(dst, file)
	}

	newRecipe := Recipe{
		ID:          len(recipes) + 1,
		Title:       title,
		Time:        time,
		Description: description,
		ImagePath:   "/" + imagePath, // prep for web path
	}

	recipes = append(recipes, newRecipe)

	http.Redirect(w, r, "/recipes", http.StatusSeeOther)
}

func editRecipeFormHandler(w http.ResponseWriter, r *http.Request) {
	idStr := mux.Vars(r)["id"]
	id, _ := strconv.Atoi(idStr)

	for _, recipe := range recipes {
		if recipe.ID == id {
			renderTemplate(w, "recipe_edit", recipe)
			return
		}
	}
	http.NotFound(w, r)
}

func updateRecipeHandler(w http.ResponseWriter, r *http.Request) {
	idStr := mux.Vars(r)["id"]
	id, _ := strconv.Atoi(idStr)

	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Could not parse form", http.StatusBadRequest)
		return
	}

	for i := range recipes {
		if recipes[i].ID == id {
			recipes[i].Title = r.FormValue("title")
			recipes[i].Time = r.FormValue("time")
			recipes[i].Description = r.FormValue("description")

			// Handle optional new image
			file, handler, err := r.FormFile("image")
			if err == nil {
				defer file.Close()
				fileName := fmt.Sprintf("%d_%s", id, handler.Filename)
				imagePath := "uploads/images/" + fileName

				dst, err := os.Create(imagePath)
				if err != nil {
					http.Error(w, "Could not save image", http.StatusInternalServerError)
					return
				}
				defer dst.Close()
				io.Copy(dst, file)

				recipes[i].ImagePath = "/" + imagePath
			}

			break
		}
	}

	http.Redirect(w, r, "/recipes/"+idStr, http.StatusSeeOther)
}
